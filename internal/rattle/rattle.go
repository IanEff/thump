package rattle

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/tracing"
	"github.com/ianeff/thump/internal/whir"
)

// Main is rattle's process entry point: parse flags and environment via
// beat.Start, wire a PromSource plus whatever topology/traffic sources
// PROM_URL and the WHIR_*/RATTLE_TRAFFIC env vars enable, and run the
// reconcile loop until the context is cancelled. It returns a process exit
// code rather than calling os.Exit, so beat.Start's flag/version handling
// stays testable.
func Main(args []string, stdout, stderr io.Writer, version, commit, date string) int {
	lc, code, exit := beat.Start("rattle", args, stdout, stderr, beat.Version{Version: version, Commit: commit, Date: date})
	if exit {
		return code
	}
	defer lc.Stop()
	ctx := lc.Ctx
	log := slog.Default()

	cfg, err := config.LoadRattle(lc.NATSURL != "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	slos, err := LoadWatch(cfg.WatchPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load watch list: %v\n", err)
		return 1
	}

	var topo TopologySource
	if cfg.WhirCatalog != "" && cfg.WhirStateQueries != "" {
		queries, err := whir.LoadStateQueries(cfg.WhirStateQueries)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load state queries: %v\n", err)
			return 1
		}
		if _, err := whir.LoadCatalogFile(cfg.WhirCatalog); err != nil {
			_, _ = fmt.Fprintf(stderr, "load whir catalog: %v\n", err)
			return 1
		}
		topo = &WhirTopologySource{Resolver: &whir.Resolver{
			BaseURL: cfg.PromURL,
			Client:  http.DefaultClient,
			Queries: queries,
		}}
	}

	var traffic TrafficSource
	if cfg.Traffic != "" {
		queries, err := LoadTrafficQueries(cfg.Traffic)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load traffic queries: %v\n", err)
			return 1
		}
		traffic = &HubbleTrafficSource{BaseURL: cfg.PromURL, Client: http.DefaultClient, Queries: queries}
	}

	_, health, shutdownMetrics := beat.Metrics("rattle")
	defer func() { _ = shutdownMetrics(ctx) }()

	var pub publish.Publisher[signal.Detection]
	var walPub *publish.WALPublisher[signal.Detection]
	if lc.NATSURL != "" {
		js, closeNC, err := broker.Connect(ctx, lc.NATSURL)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		defer closeNC()
		p, _, err := beat.NewWALPublisher[signal.Detection](js, cfg.WALDir, "rattle", "thump.detections")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		pub = p
		walPub = p
	} else if cfg.Outbox != "" {
		// offline path: the DirPublisher is now the keyless fake the seam
		// tests exercise — broker mode above is how this actually runs.
		if err := os.MkdirAll(cfg.Outbox, 0o750); err != nil { //nolint:gosec
			_, _ = fmt.Fprintf(stderr, "mkdir outbox: %v\n", err)
			return 1
		}
		pub = &publish.DirPublisher[signal.Detection]{
			Dir:  cfg.Outbox,
			Name: func(d signal.Detection) string { return d.Fingerprint },
		}
	}

	// rattle only ever publishes — it has no durable consumer of its own to
	// bind (thump.detections belongs to clank's), so a successful
	// broker.Connect above (or no broker dependency at all, offline) is the
	// entire readiness contract.
	health.SetReady(true)

	tracer, shutdownTracer, err := beat.Tracer(ctx, "rattle")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v\n", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	r := newReconciler(cfg.PromURL, slos, topo, traffic)

	if walPub != nil {
		sink, err := beat.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		defer func() { _ = walPub.WAL.Drain(ctx, sink) }()
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			beat.RunShipper(gctx, walPub.WAL, sink)
			return nil
		})
		g.Go(func() error {
			runLoop(gctx, r, log, pub, tracer)
			return nil
		})
		return beat.ExitOnError(ctx, g.Wait())
	}

	runLoop(ctx, r, log, pub, tracer)
	return 0
}

// newReconciler assembles the Reconciler Main runs — pulled out of Main so a
// test can drive it with a fake Source and prove the wiring is correct; Main
// itself is only reachable with a live PROM_URL.
func newReconciler(promURL string, slos []SLO, topo TopologySource, traffic TrafficSource) *Reconciler {
	return &Reconciler{
		SLOs:           slos,
		Source:         NewPromSource(promURL),
		Detector:       AccelerationDetector{Threshold: 0.5},
		Sustained:      &SustainedBurnDetector{Threshold: 1.0, MinSamples: 5},
		Debounce:       NewDebouncer(10 * time.Minute),
		TopologySource: topo,
		TrafficSource:  traffic,
		Contract: &SignalContract{
			FreshnessBound:  5 * time.Minute, // samples land every 1m; >5m stale = scrape path is broken
			ConfidenceFloor: 0.5,             // attenuation never drives confidence below "suspect"
		},
	}
}

// runLoop reconciles once a minute until ctx is cancelled, logging and
// publishing every detection. A Reconcile error is logged and the tick
// skipped, never fatal — the next tick tries again rather than exiting the
// process over one failed scrape.
func runLoop(ctx context.Context, r *Reconciler, log *slog.Logger, pub publish.Publisher[signal.Detection], tracer trace.Tracer) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		detections, err := r.Reconcile(ctx)
		if err != nil {
			log.Error("reconcile failed", "error", err)
		} else {
			for _, d := range detections {
				log.Info("detection",
					"name", d.Name,
					"fingerprint", d.Fingerprint,
					"detector", d.DetectorType,
					"accel", d.Divergence.Observed)
				if pub != nil {
					// rattle mints the incident's root — every downstream beat
					// only ever extracts a trace, it never mints one (see
					// internal/broker's Subscriber). One fingerprint, one
					// trace, for the detection's whole life across the wire.
					detCtx, span := tracer.Start(tracing.RootContext(ctx, d.Fingerprint), "detect")
					if err := pub.Publish(detCtx, "thump.detections", d); err != nil {
						log.Error("publish failed", "fingerprint", d.Fingerprint, "error", err)
					}
					span.End()
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
