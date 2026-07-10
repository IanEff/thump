package clank

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/broker"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// runBroker is clank's NATS branch: it consumes two inbound edges at once —
// thump.detections (reason a detection into a proposal) and thump.outcomes (the
// return edge, absorbing an outcome back into the case base) — under one
// errgroup, publishing thump.proposals. The two-subscriber shape is clank's
// own; the beat kit supplies the consumer/publisher primitives but leaves this
// composition here.
func runBroker(ctx context.Context, natsURL string, model Model, intake *Intake, store Store, tools map[string]Tool, tracer trace.Tracer, recorder *Recorder, stderr io.Writer) int {
	js, closeNC, err := broker.Connect(ctx, natsURL)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	defer closeNC()

	proposalPub, closeW, err := beat.NewWALPublisher[proposal.Set](js, os.Getenv("WAL_DIR"), "clank", "thump.proposals")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = closeW(ctx) }()

	ledger := NewMemProposalLog()
	cases := NewCaseBase()
	learn := Click{Ledger: ledger, Cases: cases, Recorder: recorder}

	eng := newBrokerEngine(model, intake, store, tools, proposalPub, ledger, cases, tracer)

	g, gctx := errgroup.WithContext(ctx)

	detSub := broker.NewJetSubscriber[signal.Detection](js)
	g.Go(func() error {
		return detSub.Run(gctx, "thump.detections", func(ctx context.Context, det signal.Detection) error {
			set, err := eng.Propose(ctx, det)
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
		return outSub.Run(gctx, "thump.outcomes", func(ctx context.Context, o outcome.Outcome) error {
			return learnHandler(ctx, learn, o) // maps Absorb's errors to Ack/transient — see below
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
