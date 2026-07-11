// Package thump is the Act beat: it renders (and, later, executes) a
// governed decision.Decision. Actuator.Render turns one approval into an
// Order, invented from nothing more than the Decision, the Set's recommended
// Candidate, and the ActionContract catalog; Executor then performs it. v1
// is structurally dry-run — DryRun is the only Executor, and an
// import-allowlist test on this package proves no code path here can reach
// os/exec, net, or a Kubernetes client, rather than merely trusting a flag
// to keep it that way.
package thump

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// Main is thump's process entry point: run either the NATS branch (consume
// thump.decisions, render + dry-run-execute, publish thump.orders and
// thump.outcomes) or the directory-poll fallback (THUMP_INBOX/THUMP_OUTBOX)
// depending on whether a NATS URL is configured. It returns a process exit
// code rather than calling os.Exit, so the whole startup path stays
// testable.
func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
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

	cat, err := contract.LoadCatalogFile(cfg.ActionCatalog, contract.Preconditions)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load action catalog: %v\n", err)
		return 1
	}

	tracer, shutdownTracer, err := beat.Tracer(ctx, "thump")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v\n", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	reg, health, shutdownMetrics := beat.Metrics("thump")
	defer func() { _ = shutdownMetrics(ctx) }()
	stages := beat.NewStageRecorder(reg)

	if lc.NATSURL != "" {
		return runBroker(ctx, lc.NATSURL, cfg, cat, tracer, stages, health, stderr)
	}
	health.SetReady(true)

	// offline path: the dir-glob Transport is now the keyless fake the seam
	// tests exercise — broker mode above is how this actually runs. THUMP_INBOX/
	// OUTBOX are this path's env, not the process's — checked here, not above,
	// so broker mode never has to satisfy them (mirrors rattle.go's NATS_URL-
	// first branch).
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
		Catalog: cat,
		Log:     NewOutcomeLog(),
		Exec:    DryRun{},
		Tracer:  tracer,
		Stages:  stages,
	}
	beat.PollLoop(ctx, beat.PollConfig{Interval: 5 * time.Second}, tr.Tick)
	return 0
}

// runBroker is thump's NATS branch: consume thump.decisions, render +
// dry-run-execute, publish thump.orders + thump.outcomes. thump.orders has no
// consumer (DurableFor("thump.orders") == "") — publishing it anyway is
// fine, WAL-only the day it stops being fine, per Ian's call.
func runBroker(ctx context.Context, natsURL string, cfg config.Thump, cat *contract.StaticCatalog, tracer trace.Tracer, stages *beat.StageRecorder, health *beat.Health, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	if err := beat.AwaitConsumers(ctx, js, health, "thump.decisions"); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	orderPub, closeOrders, err := beat.NewWALPublisher[Order](js, cfg.WALDir, "thump", "thump.orders")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeOrders(ctx) }()
	outcomePub, closeOutcomes, err := beat.NewWALPublisher[outcome.Outcome](js, cfg.WALDir, "thump", "thump.outcomes")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeOutcomes(ctx) }()

	sink, err := beat.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	tr := &Transport{
		OrderPub:   orderPub,
		OutcomePub: outcomePub,
		Catalog:    cat,
		Log:        NewOutcomeLog(),
		Exec:       DryRun{},
		Tracer:     tracer,
		Stages:     stages,
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		beat.RunShipper(gctx, orderPub.WAL, sink)
		return nil
	})
	g.Go(func() error {
		beat.RunShipper(gctx, outcomePub.WAL, sink)
		return nil
	})
	g.Go(func() error {
		return beat.RunConsumer[decision.Governed](gctx, js, "thump.decisions", tr.handle)
	})

	return beat.ExitOnError(ctx, g.Wait())
}
