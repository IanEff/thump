package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// defaultBackoff mirrors EnsureTopology's ConsumerConfig.BackOff. Plain
// Nak() ignores the consumer's configured BackOff entirely (it only applies
// to AckWait timeouts, not explicit naks), so the transient door computes
// its own delay and calls NakWithDelay instead.
var defaultBackoff = []time.Duration{time.Second, 5 * time.Second, 15 * time.Second}

// Handler processes one decoded message. Returning an error tells the
// Subscriber the delivery failed and should be retried or dead-lettered —
// it never sees an undecodable message, since that failure is caught
// before Handler runs.
type Handler[T any] func(ctx context.Context, obj T) error

// Subscriber runs h against every message on subject until ctx is
// cancelled — the inbound half of a beat's Transport, mirroring Publisher
// on the outbound side.
type Subscriber[T any] interface {
	Run(ctx context.Context, subject string, h Handler[T]) error
}

// JetSubscriber is the JetStream Subscriber. It decodes each message with
// internal/wire and dispatches it to h, then routes any failure through
// one of two doors: DOOR 1 poison — undecodable, dead-lettered on the spot,
// no retry; DOOR 2 transient — h returned an error, nak'd with Backoff's
// delay and retried until the consumer's MaxDeliver budget is spent, then
// dead-lettered too.
type JetSubscriber[T any] struct {
	js jetstream.JetStream
	// Backoff is the redelivery delay schedule, indexed by NumDelivered-1
	// (clamped to the last entry once it runs out). Defaults to
	// defaultBackoff; tests override it so they don't sit through
	// production timings.
	Backoff []time.Duration
}

// NewJetSubscriber builds a JetSubscriber with the default backoff
// schedule (1s, 5s, 15s) already set on Backoff.
func NewJetSubscriber[T any](js jetstream.JetStream) *JetSubscriber[T] {
	return &JetSubscriber[T]{js: js, Backoff: defaultBackoff}
}

// backoffFor returns the delay before the next redelivery, given how many
// times the message has already been delivered. NumDelivered is 1 on the
// first (failed) attempt, so index 0 is "the delay before attempt 2".
func (s *JetSubscriber[T]) backoffFor(numDelivered uint64) time.Duration {
	schedule := s.Backoff
	if len(schedule) == 0 {
		schedule = defaultBackoff
	}
	idx := int(numDelivered) - 1 //nolint:gosec
	if idx < 0 {
		idx = 0
	}
	if idx >= len(schedule) {
		idx = len(schedule) - 1
	}
	return schedule[idx]
}

// Run subscribes subject and dispatches every message to h until ctx is
// cancelled. A message that fails to decode is dead-lettered immediately
// (DOOR 1, no retry); one that decodes but fails h is nak'd with
// backoffFor's delay and retried until NumDelivered reaches maxDeliver,
// then dead-lettered (DOOR 2). Everything else is acked. Returns
// ctx.Err() once cancelled.
func (s *JetSubscriber[T]) Run(ctx context.Context, subject string, h Handler[T]) error {
	cons, err := s.js.Consumer(ctx, StreamName, DurableFor(subject))
	if err != nil {
		return fmt.Errorf("broker: get consumer %s: %w", subject, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		var obj T
		if err := wire.Unmarshal(msg.Data(), &obj); err != nil {
			// DOOR 1 — poison: never decodes. Dead-letter it now, no retry.
			_, _ = s.js.Publish(ctx, subject+".dlq", msg.Data())
			_ = msg.TermWithReason("undecodable")
			return
		}

		if err := h(ctx, obj); err != nil {
			// DOOR 2 — transient: handler failed. Retry with backoff until
			// the budget (maxDeliver) is spent, then dead-letter.
			md, _ := msg.Metadata()
			if md != nil && md.NumDelivered >= maxDeliver {
				_, _ = s.js.Publish(ctx, subject+".dlq", msg.Data())
				_ = msg.TermWithReason("retry budget exhausted")
				return
			}
			var delivered uint64
			if md != nil {
				delivered = md.NumDelivered
			}
			_ = msg.NakWithDelay(s.backoffFor(delivered))
			return
		}

		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("broker: consume %s: %w", subject, err)
	}
	defer cc.Stop()

	<-ctx.Done() // run until cancelled — this is the beat's main loop now
	return ctx.Err()
}
