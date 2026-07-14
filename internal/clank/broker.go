package clank

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// runBroker is clank's NATS branch: it consumes two inbound edges at once —
// thump.detections (reason a detection into a proposal) and thump.outcomes (the
// return edge, absorbing an outcome back into the case base) — under one
// errgroup, publishing thump.proposals and shipping the proposals WAL's
// sealed segments to object storage in the background. The two-subscriber
// shape is clank's own; the beat kit supplies the consumer/publisher
// primitives but leaves this composition here.
func runBroker(ctx context.Context, natsURL string, cfg config.Clank, model Model, intake *Intake, store Store, tools map[string]Tool, cat *contract.StaticCatalog, classes []contract.FailureClassDefinition, tracer trace.Tracer, recorder *Recorder, stages *beat.StageRecorder, health *beat.Health, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	if err := beat.AwaitConsumers(ctx, js, health, "thump.detections", "thump.outcomes", "thump.declines"); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err) // TODO: write error
		return 1
	}

	proposalPub, _, err := beat.NewWALPublisher[proposal.Set](js, cfg.WALDir, "clank", "thump.proposals")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	sink, err := beat.NewS3SegmentSink(ctx, cfg.S3Endpoint, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer func() { _ = proposalPub.WAL.Drain(ctx, sink) }()

	ledger := NewMemProposalLog()
	cases := NewCaseBase()
	learn := Click{Ledger: ledger, Cases: cases, Recorder: recorder}

	// HeartbeatingStore lets the detection handler below reset this run's
	// JetStream AckWait deadline on real checkpoint progress (via
	// WithHeartbeat) rather than needing engine.go's loop to know a NATS
	// message exists at all.
	eng := newBrokerEngine(model, intake, HeartbeatingStore{store}, tools, cat, classes, proposalPub, ledger, cases, tracer, stages)

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		beat.RunShipper(gctx, proposalPub.WAL, sink)
		return nil
	})

	detSub := broker.NewJetSubscriber[signal.Detection](js)
	g.Go(func() error {
		return detSub.Run(gctx, "thump.detections", func(ctx context.Context, det signal.Detection, heartbeat func()) error {
			set, err := eng.Propose(WithHeartbeat(ctx, heartbeat), det)
			if err != nil {
				return err
			}
			gatePassed := set.Gate != nil && set.Gate.Passed
			slog.Info("reasoned", "fingerprint", det.Fingerprint, "phase", set.Status.Phase, "recommended", set.Recommended, "gatePassed", gatePassed, "reason", set.Status.Reason, "evidence", len(set.Evidence))
			return nil
		})
	})

	outSub := broker.NewJetSubscriber[outcome.Outcome](js)
	g.Go(func() error {
		return outSub.Run(gctx, "thump.outcomes", func(ctx context.Context, o outcome.Outcome, _ func()) error {
			return learnHandler(ctx, learn, o) // Absorb is fast — no heartbeat needed; maps Absorb's errors to Ack/transient — see below
		})
	})

	decSub := broker.NewJetSubscriber[decision.Decision](js)
	g.Go(func() error {
		return decSub.Run(gctx, "thump.declines", func(ctx context.Context, dec decision.Decision, _ func()) error {
			return declineHandler(ctx, ledger, dec) // closes the ledger row only — never touches Click or the case base
		})
	})

	return beat.ExitOnError(ctx, g.Wait())
}

// learnHandler maps the return edge's Absorb outcomes to broker acknowledgement
// semantics: success and both expected failures (no open set, incoherent
// outcome) all Ack, because none of them get better on redelivery — erroring
// would just retry-then-dead-letter a terminal condition.
func learnHandler(ctx context.Context, c Click, o outcome.Outcome) error {
	switch err := c.Absorb(ctx, o); {
	case err == nil:
		return nil // matched + learned → Ack
	case errors.Is(err, ErrNoOpenSet):
		slog.Warn("outcome arrived with no open set", "fingerprint", o.SignalRef)
		return nil // terminal → Ack, don't retry-forever
	default: // unauditable / incoherent — deterministic, a real seam bug
		slog.Error("outcome failed absorb", "fingerprint", o.SignalRef, "err", err)
		return nil // terminal, so Ack (erroring would DLQ-after-retries)
	}
}

// declineHandler maps a governance non-approval straight to the ledger,
// mirroring learnHandler's Ack-mapping: success and ErrNoOpenSet both Ack,
// because neither improves on redelivery.
func declineHandler(ctx context.Context, ledger *MemProposalLog, dec decision.Decision) error {
	switch _, err := ledger.Decline(ctx, dec.SignalRef, dec.EvaluatedAt); {
	case err == nil:
		return nil
	case errors.Is(err, ErrNoOpenSet):
		slog.Warn("decline arrived with no open set", "fingerprint", dec.SignalRef)
		return nil
	default:
		slog.Error("decline failed", "fingerprint", dec.SignalRef, "err", err)
		return nil
	}
}
