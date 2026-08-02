// Package thump is the Act beat: it renders (dry-run, the default) or
// executes (live) an approved decision.Decision. Actuator.Render turns one
// approval into an Order, invented from nothing more than the Decision, the
// Set's recommended Candidate, and the ActionContract catalog; Executor then
// carries it out. DryRun only ever renders. Live delegates to an injected
// ActionRunner — internal/actuate is the only concrete one, and the sole
// place client-go is reachable — behind GatedExecutor, which refuses every
// forward Order while the kill switch is disarmed. Package thump itself never
// imports os/exec, net, or a Kubernetes client — an import-allowlist test on
// this package enforces that boundary directly, rather than trusting Live's
// own behavior to hold it.
package thump

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/converge"
	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/tlsx"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Main is thump's process entry point: run either the NATS branch (consume
// thump.decisions, render + dry-run-execute, publish thump.orders and
// thump.outcomes) or the directory-poll fallback (THUMP_INBOX/THUMP_OUTBOX)
// depending on whether a NATS URL is configured. It returns a process exit
// code rather than calling os.Exit, so the whole startup path stays
// testable. notifierCtor builds the concrete Notifier from a webhook URL —
// injected because internal/thump can't import internal/notify/slack itself
// (see buildNotifier); cmd/thump's composition root is the one place that can.
func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string, notifierCtor func(url string) Notifier) int {
	lc, code, exit := beat.Start("thump", args, stdout, stderr, beat.Version{Version: version, Commit: commit, Date: date})
	if exit {
		return code
	}
	defer lc.Stop()
	ctx := lc.Ctx

	cfg, err := config.LoadThump(lc.NATSURL != "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	notifier := buildNotifier(cfg, notifierCtor)

	cat, err := contract.LoadCatalogFile(cfg.ActionCatalog, contract.Preconditions)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load action catalog: %v\n", err)
		return 1
	}

	tracer, shutdownTracer, err := beat.Tracer(ctx, "thump", cfg.OTLPEndpoint, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v\n", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	var metricsTLS *tls.Config
	if cfg.TLSCertFile != "" {
		metricsTLS, err = tlsx.Server(tlsx.Config{
			CertFile: cfg.TLSCertFile,
			KeyFile:  cfg.TLSKeyFile,
			CAFile:   cfg.TLSCAFile,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "metrics tls setup: %v\n", err)
			return 1
		}
	}
	reg, health, shutdownMetrics := beat.Metrics("thump", metricsTLS)
	defer func() { _ = shutdownMetrics(ctx) }()
	stages := beat.NewStageRecorder(reg)

	if lc.NATSURL != "" {
		return runBroker(ctx, cfg.NATSURL, cfg, cat, notifier, tracer, stages, health, stderr)
	}
	health.SetReady(true)

	// offline path: the dir-glob Transport is now the keyless fake the seam
	// tests exercise — broker mode above is how this actually runs. THUMP_INBOX/
	// OUTBOX are this path's env, not the process's — checked here, not above,
	// so broker mode never has to satisfy them (mirrors rattle.go's NATS_URL-
	// first branch).
	exec, sw, err := buildExecutor(cfg, cat)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build executor: %v\n", err)
		return 1
	}
	watcher, err := buildReversalWatcher(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build reversal watcher: %v\n", err)
		return 1
	}
	tr := &Transport{
		Inbox: cfg.Inbox,
		OrderPub: &publish.DirPublisher[Order]{
			Dir:  filepath.Join(cfg.Outbox, "orders"),
			Name: func(o Order) string { return o.SignalRef },
		},
		OutcomePub: &publish.DirPublisher[outcome.Outcome]{
			Dir:  filepath.Join(cfg.Outbox, "outcomes"),
			Name: func(o outcome.Outcome) string { return o.SignalRef },
		},
		DeclinePub: &publish.DirPublisher[decision.Decision]{
			Dir:  filepath.Join(cfg.Outbox, "declines"),
			Name: func(d decision.Decision) string { return d.SignalRef },
		},
		HeldPub: &publish.DirPublisher[decision.Governed]{
			Dir:  filepath.Join(cfg.Outbox, "held"),
			Name: func(g decision.Governed) string { return g.Decision.SignalRef },
		},
		Catalog:  cat,
		Log:      NewOutcomeLog(),
		Exec:     exec,
		Reversal: watcher,
		Notifier: notifier,
		Tracer:   tracer,
		Stages:   stages,
	}
	if sw != nil {
		go beat.PollLoop(ctx, beat.DefaultPollConfig, sw.Reload)
	}
	beat.PollLoop(ctx, beat.DefaultPollConfig, tr.Tick)
	return 0
}

// runBroker is thump's NATS branch: consume thump.decisions, render +
// dry-run-execute, publish thump.orders + thump.outcomes. thump.orders has no
// consumer (DurableFor("thump.orders") == "") — publishing it anyway is
// fine, WAL-only the day it stops being fine, per Ian's call.
func runBroker(ctx context.Context, natsURL string, cfg config.Thump, cat *contract.StaticCatalog, notifier Notifier, tracer trace.Tracer, stages *beat.StageRecorder, health *beat.Health, stderr io.Writer) int {
	ctx, brokerLost := context.WithCancelCause(ctx)
	defer brokerLost(nil)

	js, closeNC, err := broker.Connect(ctx, natsURL, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	}, beat.BrokerHooks(health, "thump", func() { brokerLost(beat.ErrBrokerClosed) }))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	if err := beat.AwaitConsumers(ctx, js, health, "thump.decisions"); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	walConfig, err := beat.LoadWALConfig(cfg.WALConfig)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load wal config: %v\n", err)
		return 1
	}

	orderPub, _, err := beat.NewWALPublisher[Order](js, cfg.WALDir, "thump", "thump.orders", walConfig)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	outcomePub, _, err := beat.NewWALPublisher[outcome.Outcome](js, cfg.WALDir, "thump", "thump.outcomes", walConfig)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	declinePub, _, err := beat.NewWALPublisher[decision.Decision](js, cfg.WALDir, "thump", "thump.declines", walConfig)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	heldPub, _, err := beat.NewWALPublisher[decision.Governed](js, cfg.WALDir, "thump", "thump.held", walConfig)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	sink, err := beat.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey, sealbox.Key(cfg.SealKey))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer func() { _ = orderPub.WAL.Drain(ctx, sink) }()
	defer func() { _ = outcomePub.WAL.Drain(ctx, sink) }()
	defer func() { _ = declinePub.WAL.Drain(ctx, sink) }()
	defer func() { _ = heldPub.WAL.Drain(ctx, sink) }()

	exec, sw, err := buildExecutor(cfg, cat)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build executor: %v\n", err)
		return 1
	}
	watcher, err := buildReversalWatcher(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build reversal watcher: %v\n", err)
		return 1
	}
	tr := &Transport{
		OrderPub:   orderPub,
		OutcomePub: outcomePub,
		DeclinePub: declinePub,
		HeldPub:    heldPub,
		Catalog:    cat,
		Log:        NewOutcomeLog(),
		Exec:       exec,
		Reversal:   watcher,
		Notifier:   notifier,
		Tracer:     tracer,
		Stages:     stages,
	}

	g, gctx := errgroup.WithContext(ctx)
	if sw != nil {
		g.Go(func() error {
			beat.PollLoop(gctx, beat.DefaultPollConfig, sw.Reload)
			return nil
		})
	}
	g.Go(func() error {
		beat.RunShipper(gctx, orderPub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		beat.RunShipper(gctx, outcomePub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		beat.RunShipper(gctx, declinePub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		beat.RunShipper(gctx, heldPub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		return beat.RunConsumer[decision.Governed](gctx, js, "thump.decisions", tr.handle)
	})

	return beat.ExitOnError(ctx, g.Wait())
}

// buildExecutor picks the executor from cfg.Executor — dry (the default)
// renders; live wraps a real actuate.Runner in a GatedExecutor so an armed
// kill-switch is required before anything touches infrastructure. The
// returned *FileSwitch is nil in dry mode — nothing to reload.
func buildExecutor(cfg config.Thump, cat *contract.StaticCatalog) (Executor, *FileSwitch, error) {
	if cfg.Executor != "live" {
		return DryRun{}, nil, nil
	}
	runner, err := actuate.New(cat)
	if err != nil {
		return nil, nil, fmt.Errorf("build live executor: %w", err)
	}
	sw := NewFileSwitch(cfg.KillSwitchPath)
	return GatedExecutor{
		Inner:  Live{Runner: runner},
		Switch: sw,
	}, sw, nil
}

// buildReversalWatcher wires the automatic-undo probe from cfg. backendTLS is
// nil in the offline path (cfg.TLSCertFile unset) and dials PROM_URL in the
// clear, same as today — L4/L5's declared exception. In the broker path it's
// the beat's own leaf, ready the day Prometheus starts serving TLS from the
// cluster's private CA.
func buildReversalWatcher(cfg config.Thump) (*ReversalWatcher, error) {
	if cfg.PromURL == "" {
		return nil, nil
	}
	queries, err := converge.LoadQueries(cfg.EvidenceQueries)
	if err != nil {
		return nil, fmt.Errorf("load evidence queries: %w", err)
	}
	var backendTLS *tls.Config
	if cfg.TLSCertFile != "" {
		backendTLS, err = tlsx.Client(tlsx.Config{
			CertFile: cfg.TLSCertFile,
			KeyFile:  cfg.TLSKeyFile,
			CAFile:   cfg.TLSCAFile,
		})
		if err != nil {
			return nil, fmt.Errorf("backend tls setup: %w", err)
		}
	}
	prober := &converge.Prober{BaseURL: cfg.PromURL, Queries: queries, Client: httpx.Client(httpx.DefaultBackendTimeout, backendTLS)}
	return &ReversalWatcher{Probe: PrometheusConverger{Probe: prober}}, nil
}

// buildNotifier turns cfg's Slack webhook URL into a Notifier via ctor.
// Unlike buildExecutor/buildReversalWatcher above, this package never
// constructs the concrete client itself — ctor is supplied by cmd/thump's
// composition root, which is free to import the Slack package; an empty URL
// (SLACK_WEBHOOK_URL unset) means no notifier, not a broken one — a hold
// still publishes to HeldPub, it just pages nobody (handle nil-checks
// Notifier at transport.go:161).
func buildNotifier(cfg config.Thump, ctor func(url string) Notifier) Notifier {
	if cfg.SlackWebhookURL == "" {
		slog.Warn("no Slack webhook configured - held actions will page nobody", "beat", "thump", "fix", "set SLACK_WEBHOOK_URL")
		return nil
	}
	return ctor(cfg.SlackWebhookURL)
}
