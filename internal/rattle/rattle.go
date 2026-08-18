package rattle

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/httpx"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/otelx"
	"github.com/ianeff/thump/internal/poll"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/tlsx"
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
		log.Error("load config", "err", err)
		return 1
	}

	slos, err := LoadWatch(cfg.WatchPath)
	if err != nil {
		log.Error("load watch list", "err", err)
		return 1
	}

	query, err := LoadQueryConfig(cfg.QueryConfig)
	if err != nil {
		log.Error("load query config", "err", err)
		return 1
	}

	// backendTLS is nil in the offline path (cfg.TLSCertFile unset) and dials
	// PROM_URL in the clear, same as today — L4/L5's declared exception. In
	// the broker path it's the beat's own leaf, ready the day Prometheus
	// starts serving TLS from the cluster's private CA.
	var backendTLS *tls.Config
	if cfg.TLSCertFile != "" {
		backendTLS, err = tlsx.Client(tlsx.Config{
			CertFile: cfg.TLSCertFile,
			KeyFile:  cfg.TLSKeyFile,
			CAFile:   cfg.TLSCAFile,
		})
		if err != nil {
			slog.Error("backend tls setup", "err", err)
			return 1
		}
	}

	topo, traffic, err := buildSources(cfg, backendTLS)
	if err != nil {
		slog.Error("build sources", "err", err)
		return 1
	}

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
	reg, health, shutdownMetrics := beat.Metrics("rattle", metricsTLS)
	stages := beat.NewStageRecorder(reg)
	defer func() { _ = shutdownMetrics(ctx) }()

	ctx, brokerLost := context.WithCancelCause(ctx)
	defer brokerLost(nil)

	var pub publish.Publisher[signal.Detection]
	var walPub *publish.WALPublisher[signal.Detection]
	var walConfig beat.WALConfig
	if lc.NATSURL != "" {
		js, closeNC, err := broker.Connect(ctx, cfg.NATSURL, tlsx.Config{
			CertFile: cfg.TLSCertFile,
			KeyFile:  cfg.TLSKeyFile,
			CAFile:   cfg.TLSCAFile,
		}, beat.BrokerHooks(health, "rattle", func() { brokerLost(beat.ErrBrokerClosed) }))
		if err != nil {
			slog.Error("connect broker", "err", err)
			return 1
		}
		defer closeNC()
		walConfig, err = beat.LoadWALConfig(cfg.WALConfig)
		if err != nil {
			slog.Error("load wal config", "err", err)
			return 1
		}
		p, _, err := beat.NewWALPublisher[signal.Detection](js, cfg.WALDir, "rattle", "thump.detections", walConfig)
		if err != nil {
			slog.Error("wal publisher", "err", err)
			return 1
		}
		pub = p
		walPub = p
	} else if cfg.Outbox != "" {
		// offline path: the DirPublisher is now the keyless fake the seam
		// tests exercise — broker mode above is how this actually runs.
		if err := os.MkdirAll(cfg.Outbox, 0o750); err != nil { //nolint:gosec // G301: operator-configured directory, not user input
			slog.Error("mkdir outbox", "err", err)
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

	tracer, shutdownTracer, err := otelx.Tracer(ctx, "rattle", cfg.OTLPEndpoint, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	})
	if err != nil {
		slog.Error("tracer setup", "err", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	r := newReconciler(cfg.PromURL, slos, topo, traffic, backendTLS, query)

	if walPub != nil {
		sink, err := objectstore.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey, sealbox.Key(cfg.SealKey))
		if err != nil {
			slog.Error("s3 segment sink", "err", err)
			return 1
		}
		defer func() {
			if err := walPub.WAL.Drain(ctx, sink); err != nil {
				slog.Error("failed to drain WAL", "err", err)
			}
		}()
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			publish.RunShipper(gctx, walPub.WAL, sink, walConfig.ShipInterval)
			return nil
		})
		g.Go(func() error {
			runLoop(gctx, r, log, pub, tracer, stages, query.PollInterval, query.ReconcileTimeout)
			return nil
		})
		return beat.ExitOnError(ctx, g.Wait())
	}

	runLoop(ctx, r, log, pub, tracer, stages, query.PollInterval, query.ReconcileTimeout)
	return 0
}

// newReconciler assembles the Reconciler Main runs — pulled out of Main so a
// test can drive it with a fake Source and prove the wiring is correct; Main
// itself is only reachable with a live PROM_URL. backendTLS is nil offline
// and non-nil in the broker path (see Main) — NewPromSource's own client
// only applies when it's nil.
func newReconciler(promURL string, slos []SLO, topo TopologySource, traffic TrafficSource, backendTLS *tls.Config, query QueryConfig) *Reconciler {
	src := NewPromSource(promURL)
	src.Step = query.Step
	src.Window = query.Window
	if backendTLS != nil {
		src.Client = httpx.Client(httpx.DefaultBackendTimeout, backendTLS)
	}
	return &Reconciler{
		SLOs:           slos,
		Source:         src,
		Detector:       AccelerationDetector{Threshold: 0.5},
		Sustained:      &SustainedBurnDetector{Threshold: 1.0, MinSamples: query.SustainedMinSamples},
		Debounce:       NewDebouncer(query.Debounce),
		TopologySource: topo,
		TrafficSource:  traffic,
		Contract: &SignalContract{
			FreshnessBound:  query.FreshnessBound,
			ConfidenceFloor: 0.5, // attenuation never drives confidence below "suspect"
		},
	}
}

// runLoop reconciles every pollInterval until ctx is cancelled, logging and
// publishing every detection. A Reconcile error is logged and the tick
// skipped, never fatal — the next tick tries again rather than exiting the
// process over one failed scrape. Single-threaded by construction — tick N+1
// never starts until tick N returns, which is why reconcileTimeout exists:
// without it, one stalled Prometheus call hangs every SLO's detection until
// SIGTERM. Caller keeps reconcileTimeout under pollInterval or ticks
// overlap.
func runLoop(ctx context.Context, r *Reconciler, log *slog.Logger, pub publish.Publisher[signal.Detection], tracer trace.Tracer, stages *beat.StageRecorder, pollInterval, reconcileTimeout time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		var detections []signal.Detection
		err := beat.Stage(ctx, tracer, stages, "reconcile", poll.WithTimeout(reconcileTimeout, func(ctx context.Context) error {
			var rErr error
			detections, rErr = r.Reconcile(ctx)
			return rErr
		}))
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

// buildSources assembles rattle's topology and traffic enrichment from cfg —
// either return can be nil, since reconcile.go's Reconciler treats both as
// optional, nil-safe enrichment, never a required collaborator.
func buildSources(cfg config.Rattle, backendTLS *tls.Config) (TopologySource, TrafficSource, error) {
	var topo TopologySource
	if cfg.WhirCatalog == "" || cfg.WhirStateQueries == "" {
		slog.Warn("no topology source configured — reconciling without a blast-radius map",
			"fix", "set WHIR_CATALOG and WHIR_STATE_QUERIES")
	} else {
		queries, err := whir.LoadStateQueries(cfg.WhirStateQueries)
		if err != nil {
			return nil, nil, fmt.Errorf("load state queries: %w", err)
		}
		if _, err := whir.LoadCatalogFile(cfg.WhirCatalog); err != nil {
			return nil, nil, fmt.Errorf("load whir catalog: %w", err)
		}
		topo = &WhirTopologySource{Resolver: &whir.Resolver{
			BaseURL: cfg.PromURL,
			Client:  httpx.Client(httpx.DefaultBackendTimeout, backendTLS),
			Queries: queries,
		}}
	}

	var traffic TrafficSource
	if cfg.Traffic == "" {
		slog.Warn("no traffic source configured — reconciling without traffic enrichment",
			"fix", "set RATTLE_TRAFFIC")
	} else {
		queries, err := LoadTrafficQueries(cfg.Traffic)
		if err != nil {
			return nil, nil, fmt.Errorf("load traffic queries: %w", err)
		}
		traffic = &HubbleTrafficSource{BaseURL: cfg.PromURL, Client: httpx.Client(httpx.DefaultBackendTimeout, backendTLS), Queries: queries}
	}
	return topo, traffic, nil
}
