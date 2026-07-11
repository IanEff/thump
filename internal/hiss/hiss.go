// Package hiss is the Governance Plane: one authority pass over a delivered
// proposal.Set, checking a confidence floor, an authority ceiling, an
// irreversibility veto, and freeze windows before clank's recommended
// Candidate may proceed. It never mutates or re-ranks the Set it reads —
// Authority.Evaluate turns each Set into exactly one decision.Decision:
// approved, escalate, or rejected. Rejection is an audit record, never
// silence.
package hiss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/publish"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/yaml"
)

// Main is hiss's process entry point: load Policy from HISS_POLICY, then run
// either the NATS branch (consume thump.proposals, evaluate, publish
// thump.decisions) or the directory-poll fallback (HISS_INBOX/HISS_OUTBOX)
// depending on whether a NATS URL is configured. It returns a process exit
// code rather than calling os.Exit, so the whole startup path stays testable.
func Main(args []string, stdout io.Writer, stderr io.Writer, version, commit, date string) int {
	lc, code, exit := beat.Start("hiss", args, stdout, stderr, beat.Version{Version: version, Commit: commit, Date: date})
	if exit {
		return code
	}
	defer lc.Stop()
	ctx := lc.Ctx

	cfg, err := config.LoadHiss(lc.NATSURL != "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	pol, err := loadPolicy(cfg.Policy)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "failed to load policy: %v\n", err)
		return 1
	}

	tracer, shutdownTracer, err := beat.Tracer(ctx, "hiss")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v", err)
		return 1
	}
	defer func() { _ = shutdownTracer(ctx) }()

	reg, health, shutdownMetrics := beat.Metrics("hiss")
	defer func() { _ = shutdownMetrics(ctx) }()
	stages := beat.NewStageRecorder(reg)

	if lc.NATSURL != "" {
		return runBroker(ctx, lc.NATSURL, cfg, pol, tracer, stages, health, stderr)
	}
	health.SetReady(true)

	// offline path: the dir-glob Transport is now the keyless fake the seam
	// tests exercise — broker mode above is how this actually runs.
	// cfg.Inbox/Outbox are this path's env, not the process's — config.LoadHiss
	// only requires them when broker is false (mirrors clank.go/rattle.go/
	// thump.go's NATS_URL-first branch).
	tr := &Transport{
		Inbox: cfg.Inbox,
		Pub: &publish.DirPublisher[decision.Governed]{
			Dir:  cfg.Outbox,
			Name: func(g decision.Governed) string { return g.Decision.SignalRef },
		},
		Policy: pol,
		Log:    NewDecisionLog(),
		Tracer: tracer,
		Stages: stages,
	}
	beat.PollLoop(ctx, beat.PollConfig{Interval: 5 * time.Second}, tr.Tick)
	return 0
}

// runBroker is hiss's NATS branch: consume thump.proposals, evaluate
// authority, publish thump.decisions, and ship the decisions WAL's sealed
// segments to object storage in the background.
func runBroker(ctx context.Context, natsURL string, cfg config.Hiss, pol Policy, tracer trace.Tracer, stages *beat.StageRecorder, health *beat.Health, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	if err := beat.AwaitConsumers(ctx, js, health, "thump.proposals"); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err) // TODO: write error message
		return 1
	}

	pub, closeW, err := beat.NewWALPublisher[decision.Governed](js, cfg.WALDir, "hiss", "thump.decisions")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeW(ctx) }()

	sink, err := beat.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	tr := &Transport{Pub: pub, Policy: pol, Log: NewDecisionLog(), Tracer: tracer, Stages: stages}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		beat.RunShipper(gctx, pub.WAL, sink)
		return nil
	})
	g.Go(func() error {
		return beat.RunConsumer[proposal.Set](gctx, js, "thump.proposals", tr.handle)
	})

	return beat.ExitOnError(ctx, g.Wait())
}

// loadPolicy reads HISS_POLICY as a YAML file and unmarshals it into a
// Policy. A missing path, an unreadable file, and a malformed file all
// fail the same way: a governor that started with a zero-value Policy
// would fail *closed* (MaxBand empty everywhere ⇒ everything escalates)
// but silently — refusing to start and saying why beats that.
func loadPolicy(path string) (Policy, error) {
	if path == "" {
		return Policy{}, errors.New("HISS_POLICY is required")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config path, not user input
	if err != nil {
		return Policy{}, fmt.Errorf("read policy file: %w", err)
	}
	var pol Policy
	if err := yaml.Unmarshal(raw, &pol); err != nil {
		return Policy{}, fmt.Errorf("parse policy file: %w", err)
	}
	return pol, nil
}
