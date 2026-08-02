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
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/health"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/poll"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/tlsx"
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

	pol, err := LoadPolicy(cfg.Policy)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "failed to load policy: %v\n", err)
		return 1
	}

	tracer, shutdownTracer, err := beat.Tracer(ctx, "hiss", cfg.OTLPEndpoint, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "tracer setup: %v", err)
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
	reg, health, shutdownMetrics := beat.Metrics("hiss", metricsTLS)
	defer func() { _ = shutdownMetrics(ctx) }()
	stages := beat.NewStageRecorder(reg)

	if lc.NATSURL != "" {
		return runBroker(ctx, cfg.NATSURL, cfg, pol, tracer, stages, health, stderr)
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
	poll.Loop(ctx, poll.DefaultConfig, tr.Tick)
	return 0
}

// runBroker is hiss's NATS branch: consume thump.proposals, evaluate
// authority, publish thump.decisions, and ship the decisions WAL's sealed
// segments to object storage in the background.
func runBroker(ctx context.Context, natsURL string, cfg config.Hiss, pol Policy, tracer trace.Tracer, stages *beat.StageRecorder, health *health.Health, stderr io.Writer) int {
	ctx, brokerLost := context.WithCancelCause(ctx)
	defer brokerLost(nil)

	js, closeNC, err := broker.Connect(ctx, natsURL, tlsx.Config{
		CertFile: cfg.TLSCertFile,
		KeyFile:  cfg.TLSKeyFile,
		CAFile:   cfg.TLSCAFile,
	}, beat.BrokerHooks(health, "hiss", func() { brokerLost(beat.ErrBrokerClosed) }))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	if err := beat.AwaitConsumers(ctx, js, health, "thump.proposals"); err != nil {
		slog.Error("await consumers failed", "err", err)
		return 1
	}

	walConfig, err := beat.LoadWALConfig(cfg.WALConfig)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load wal config: %v\n", err)
		return 1
	}

	pub, _, err := beat.NewWALPublisher[decision.Governed](js, cfg.WALDir, "hiss", "thump.decisions", walConfig)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	sink, err := objectstore.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey, sealbox.Key(cfg.SealKey))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer func() { _ = pub.WAL.Drain(ctx, sink) }()

	approvePub := publish.NewJetPublisher[approval.Approval](js)
	controller, err := buildApprovalRequests(cfg, approvePub)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	// Assigned through a nil check rather than directly: a nil
	// *ApprovalRequestController stored in an ApprovalRequests interface is
	// non-nil at the interface, and Transport.handle's nil check would let it
	// through to a method call on nothing.
	var approvals ApprovalRequests
	if controller != nil {
		approvals = controller
	}

	tr, err := buildTransport(ctx, js, pub, pol, approvals, tracer, stages)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		publish.RunShipper(gctx, pub.WAL, sink, walConfig.ShipInterval)
		return nil
	})
	g.Go(func() error {
		return beat.RunConsumer[proposal.Set](gctx, js, "thump.proposals", tr.handle)
	})
	approvalSub := broker.NewJetSubscriber[approval.Approval](js)
	g.Go(func() error {
		return approvalSub.Run(gctx, "thump.approvals", tr.approveHandler)
	})
	if controller != nil {
		g.Go(func() error {
			return controller.Run(gctx)
		})
	}

	return beat.ExitOnError(ctx, g.Wait())
}

// buildTransport assembles runBroker's Transport, seeding Holds from
// rebuildHolds rather than an empty PendingHolds — W0a's row 5:
// PendingHolds is restart-lossy unless the composition root actually
// replays thump.decisions. Extracted so a test can pin that a
// fully-configured broker path reaches the real rebuild, the same guard
// W1 wrote for clank's noopChange after finding it wired to nothing.
func buildTransport(ctx context.Context, js jetstream.JetStream, pub publish.Publisher[decision.Governed], pol Policy, approvals ApprovalRequests, tracer trace.Tracer, stages *beat.StageRecorder) (*Transport, error) {
	holds, err := rebuildHolds(ctx, js)
	if err != nil {
		return nil, fmt.Errorf("hiss: build transport: %w", err)
	}
	return &Transport{Pub: pub, Policy: pol, Log: NewDecisionLog(), Holds: holds, Approvals: approvals, Tracer: tracer, Stages: stages}, nil
}

// buildApprovalRequests returns the ApprovalRequest controller, or nil when
// the CR surface is switched off — in which case trim over thump.approvals is
// the only way to release a hold, and a held action waits on an operator
// running a command rather than patching a resource. Returns nil, not a
// no-op: Transport.Approvals is already nil-safe, and a beat that cannot
// reach Kubernetes should say so once rather than fail to start.
func buildApprovalRequests(cfg config.Hiss, approvePub publish.Publisher[approval.Approval]) (*ApprovalRequestController, error) {
	if !cfg.ApprovalRequestsEnabled {
		slog.Warn("no ApprovalRequest surface configured — holds are released through trim only",
			"beat", "hiss", "fix", "set APPROVALREQUESTS_ENABLED=true")
		return nil, nil
	}
	return NewApprovalRequestController(approvePub, cfg.ApprovalRetention)
}

// LoadPolicy reads path as a YAML file and unmarshals it into a Policy — the
// only decoder for the governance surface, so a test asserting what policy
// says is reading what hiss will actually govern under. A missing path, an
// unreadable file, and a malformed file all fail the same way: a governor
// that started with a zero-value Policy would fail *closed* (MaxBand empty
// everywhere ⇒ everything escalates) but silently — refusing to start and
// saying why beats that.
func LoadPolicy(path string) (Policy, error) {
	if path == "" {
		return Policy{}, errors.New("policy path is required")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied policy file path, not user input
	if err != nil {
		return Policy{}, fmt.Errorf("read policy file: %w", err)
	}
	var pol Policy
	if err := yaml.Unmarshal(raw, &pol); err != nil {
		return Policy{}, fmt.Errorf("parse policy file: %w", err)
	}
	return pol, nil
}
