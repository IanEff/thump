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
	"github.com/ianeff/thump/internal/health"
	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/otelx"
	"github.com/ianeff/thump/internal/poll"
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
func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string, notifierCtor func(url string) Notifier, forgeCtor func(repo, token string) Forge) int {
	lc, code, exit := beat.Start("thump", args, stdout, stderr, beat.Version{Version: version, Commit: commit, Date: date})
	if exit {
		return code
	}
	defer lc.Stop()
	ctx := lc.Ctx

	cfg, err := config.LoadThump(lc.NATSURL != "")
	if err != nil {
		slog.Error("load config", "err", err)
		return 1
	}
	notifier := buildNotifier(cfg, notifierCtor)
	f := buildForge(cfg, forgeCtor)

	cat, err := contract.LoadCatalogFile(cfg.ActionCatalog, contract.Preconditions)
	if err != nil {
		slog.Error("load action catalog", "err", err)
		return 1
	}

	tracer, shutdownTracer, err := otelx.Tracer(ctx, "thump", cfg.OTLPEndpoint, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	})
	if err != nil {
		slog.Error("tracer setup", "err", err)
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
			slog.Error("metrics tls setup", "err", err)
			return 1
		}
	}
	reg, health, shutdownMetrics := beat.Metrics("thump", metricsTLS)
	defer func() { _ = shutdownMetrics(ctx) }()
	stages := beat.NewStageRecorder(reg)

	if lc.NATSURL != "" {
		return runBroker(ctx, cfg.NATSURL, cfg, cat, notifier, f, tracer, stages, health)
	}
	health.SetReady(true)

	// offline path: the dir-glob Transport is now the keyless fake the seam
	// tests exercise — broker mode above is how this actually runs. THUMP_INBOX/
	// OUTBOX are this path's env, not the process's — checked here, not above,
	// so broker mode never has to satisfy them (mirrors rattle.go's NATS_URL-
	// first branch).
	exec, sw, err := buildExecutor(cfg, cat, f)
	if err != nil {
		slog.Error("build executor", "err", err)
		return 1
	}
	watcher, err := buildReversalWatcher(cfg)
	if err != nil {
		slog.Error("build reversal watcher", "err", err)
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
	if f != nil {
		tr.Acceptance = &AcceptanceWatcher{Probe: f}
	}
	if lc.Once {
		// A reload failure leaves sw disarmed (FileSwitch's own fail-safe) —
		// logged, not fatal, the same posture poll.Loop(sw.Reload) has in
		// every other run mode. A missing killswitch file should block a
		// live Order, not the whole diagnostic pass.
		if sw != nil {
			if err := sw.Reload(ctx); err != nil {
				slog.Warn("kill switch reload failed, staying disarmed", "err", err)
			}
		}
		if err := tr.Tick(ctx); err != nil {
			slog.Error("tick", "err", err)
			return 1
		}
		return 0
	}
	if sw != nil {
		go poll.Loop(ctx, poll.DefaultConfig, sw.Reload)
	}
	poll.Loop(ctx, poll.DefaultConfig, tr.Tick)
	return 0
}

// runBroker is thump's NATS branch: consume thump.decisions, render +
// dry-run-execute, publish thump.orders + thump.outcomes. thump.orders has no
// consumer (DurableFor("thump.orders") == "") — publishing it anyway is
// fine, WAL-only the day it stops being fine, per Ian's call.
func runBroker(ctx context.Context, natsURL string, cfg config.Thump, cat *contract.StaticCatalog, notifier Notifier, f Forge, tracer trace.Tracer, stages *beat.StageRecorder, health *health.Health) int {
	ctx, brokerLost := context.WithCancelCause(ctx)
	defer brokerLost(nil)

	js, closeNC, err := broker.Connect(ctx, natsURL, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	}, beat.BrokerHooks(health, func() { brokerLost(beat.ErrBrokerClosed) }))
	if err != nil {
		slog.Error("connect broker", "err", err)
		return 1
	}
	defer closeNC()

	if err := beat.AwaitConsumers(ctx, js, health, "thump.decisions"); err != nil {
		slog.Error("await consumers", "err", err)
		return 1
	}

	walConfig, err := beat.LoadWALConfig(cfg.WALConfig)
	if err != nil {
		slog.Error("load wal config", "err", err)
		return 1
	}

	orderPub, _, err := beat.NewWALPublisher[Order](js, cfg.WALDir, "thump", "thump.orders", walConfig)
	if err != nil {
		slog.Error("order wal publisher", "err", err)
		return 1
	}
	outcomePub, _, err := beat.NewWALPublisher[outcome.Outcome](js, cfg.WALDir, "thump", "thump.outcomes", walConfig)
	if err != nil {
		slog.Error("outcome wal publisher", "err", err)
		return 1
	}
	declinePub, _, err := beat.NewWALPublisher[decision.Decision](js, cfg.WALDir, "thump", "thump.declines", walConfig)
	if err != nil {
		slog.Error("decline wal publisher", "err", err)
		return 1
	}
	heldPub, _, err := beat.NewWALPublisher[decision.Governed](js, cfg.WALDir, "thump", "thump.held", walConfig)
	if err != nil {
		slog.Error("held wal publisher", "err", err)
		return 1
	}

	sink, err := objectstore.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey, sealbox.Key(cfg.SealKey))
	if err != nil {
		slog.Error("s3 segment sink", "err", err)
		return 1
	}
	defer func() {
		if err := orderPub.WAL.Drain(ctx, sink); err != nil {
			slog.Error("failed to drain order WAL", "error", err)
		}
	}()
	defer func() {
		if err := outcomePub.WAL.Drain(ctx, sink); err != nil {
			slog.Error("failed to drain outcome WAL", "error", err)
		}
	}()
	defer func() {
		if err := declinePub.WAL.Drain(ctx, sink); err != nil {
			slog.Error("failed to drain decline WAL", "error", err)
		}
	}()
	defer func() {
		if err := heldPub.WAL.Drain(ctx, sink); err != nil {
			slog.Error("failed to drain held WAL", "error", err)
		}
	}()

	exec, sw, err := buildExecutor(cfg, cat, f)
	if err != nil {
		slog.Error("build executor", "err", err)
		return 1
	}
	watcher, err := buildReversalWatcher(cfg)
	if err != nil {
		slog.Error("build reversal watcher", "err", err)
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
	if f != nil {
		tr.Acceptance = &AcceptanceWatcher{Probe: f}
	}

	g, gctx := errgroup.WithContext(ctx)
	if sw != nil {
		g.Go(func() error {
			poll.Loop(gctx, poll.DefaultConfig, sw.Reload)
			return nil
		})
	}
	g.Go(func() error {
		publish.RunShipper(gctx, orderPub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		publish.RunShipper(gctx, outcomePub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		publish.RunShipper(gctx, declinePub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		publish.RunShipper(gctx, heldPub.WAL, sink, walConfig.ShipInterval)
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
func buildExecutor(cfg config.Thump, cat *contract.StaticCatalog, f Forge) (Executor, *FileSwitch, error) {
	if cfg.Executor != "live" {
		return DryRun{}, nil, nil
	}
	runner, err := actuate.New(cat, f)
	if err != nil {
		return nil, nil, fmt.Errorf("build live executor: %w", err)
	}
	exec, sw := gatedLive(cfg, runner)
	return exec, sw, nil
}

// buildLiveExecutorForTest is buildExecutor's live branch, reached through
// actuate.NewWithKube instead of actuate.New so a test can prove the shipped
// catalog binds through production's own bind logic without an in-cluster
// config to satisfy New's first step. Only BuildExecutorForTest calls this.
func buildLiveExecutorForTest(cfg config.Thump, cat *contract.StaticCatalog, f Forge, k actuate.Kube) (Executor, *FileSwitch, error) {
	if cfg.Executor != "live" {
		return DryRun{}, nil, nil
	}
	runner, err := actuate.NewWithKube(k, cat, f)
	if err != nil {
		return nil, nil, fmt.Errorf("build live executor: %w", err)
	}
	exec, sw := gatedLive(cfg, runner)
	return exec, sw, nil
}

func gatedLive(cfg config.Thump, runner *actuate.Runner) (Executor, *FileSwitch) {
	sw := NewFileSwitch(cfg.KillSwitchPath)
	return GatedExecutor{
		Inner:  Live{Runner: runner},
		Switch: sw,
	}, sw
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
		slog.Warn("no Slack webhook configured - held actions will page nobody", "fix", "set SLACK_WEBHOOK_URL")
		return nil
	}
	return ctor(cfg.SlackWebhookURL)
}

func buildForge(cfg config.Thump, ctor func(repo, token string) Forge) Forge {
	if cfg.ForgeRepo == "" || cfg.ForgeToken == "" {
		slog.Warn("no Forge configured -  maintenance releases will refuse to bind", "fix", "set FORGE_REPO and FORGE_TOKEN")
		return nil
	}
	return ctor(cfg.ForgeRepo, cfg.ForgeToken)
}
