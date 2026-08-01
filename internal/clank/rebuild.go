package clank

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// buildLedger is the composition-root seam runBroker calls to construct its
// ledger — reach for this, never NewMemProposalLog() directly, in broker
// mode: the bare constructor starts empty on every restart, silently
// discarding every open set's dedup state.
func buildLedger(ctx context.Context, js jetstream.JetStream, retention time.Duration) (*MemProposalLog, error) {
	return rebuildLedger(ctx, js, retention)
}

// rebuildLedger replays clank's own thump.proposals, thump.outcomes, and
// thump.declines history and returns a MemProposalLog seeded with the
// result — rebuilding what a restart would otherwise drop, bounded by the
// shared stream's 48h retention, not a permanent archive. Events replay in
// the order JetStream stored them, through the ledger's own Observe/Decline,
// so a set's final phase matches what live processing would have produced —
// order matters because both methods act on "the most recently recorded
// open set" for a fingerprint, not a fixed one.
func rebuildLedger(ctx context.Context, js jetstream.JetStream, retention time.Duration) (*MemProposalLog, error) {
	proposals, err := fetchSubject[proposal.Set](ctx, js, "thump.proposals")
	if err != nil {
		return nil, fmt.Errorf("clank: rebuild ledger: %w", err)
	}
	outcomes, err := fetchSubject[outcome.Outcome](ctx, js, "thump.outcomes")
	if err != nil {
		return nil, fmt.Errorf("clank: rebuild ledger: %w", err)
	}
	declines, err := fetchSubject[decision.Decision](ctx, js, "thump.declines")
	if err != nil {
		return nil, fmt.Errorf("clank: rebuild ledger: %w", err)
	}

	events := make([]replayEvent, 0, len(proposals)+len(outcomes)+len(declines))
	for _, p := range proposals {
		events = append(events, replayEvent{at: p.at, proposal: &p.v})
	}
	for _, o := range outcomes {
		events = append(events, replayEvent{at: o.at, outcome: &o.v})
	}
	for _, d := range declines {
		events = append(events, replayEvent{at: d.at, decline: &d.v})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })

	ledger := NewMemProposalLog()
	ledger.Retention = retention
	for _, ev := range events {
		if err := ev.replayInto(ctx, ledger); err != nil {
			return nil, fmt.Errorf("clank: rebuild ledger: replay: %w", err)
		}
	}
	return ledger, nil
}

// replayEvent is one historical message from thump.proposals, thump.outcomes,
// or thump.declines, tagged by which pointer is non-nil — exactly one of the
// three, since a single JetStream message decodes as a single boundary type.
type replayEvent struct {
	at       time.Time
	proposal *proposal.Set
	outcome  *outcome.Outcome
	decline  *decision.Decision
}

// replayInto applies ev to ledger through the same methods live processing
// uses — seedAt for a proposal, Observe/Decline for an outcome or decline —
// so replay can never diverge from the state machine it reconstructs.
// ErrNoOpenSet is expected here exactly as it is live (learnHandler,
// declineHandler): an outcome or decline whose proposal fell outside the
// stream's retention window answers to nothing, and that's not a rebuild
// failure.
func (ev replayEvent) replayInto(ctx context.Context, ledger *MemProposalLog) error {
	switch {
	case ev.proposal != nil:
		ledger.seedAt(*ev.proposal, ev.at)
		return nil
	case ev.outcome != nil:
		_, err := ledger.Observe(ctx, *ev.outcome)
		if err != nil && !errors.Is(err, ErrNoOpenSet) {
			return err
		}
		return nil
	default:
		_, err := ledger.Decline(ctx, ev.decline.SignalRef, ev.decline.EvaluatedAt)
		if err != nil && !errors.Is(err, ErrNoOpenSet) {
			return err
		}
		return nil
	}
}

// timestamped pairs a decoded boundary object with the JetStream-assigned
// time it was stored — replay's only source of ordering, since none of
// proposal.Set/outcome.Outcome/decision.Decision carry a timestamp clank
// itself stamped at Record time.
type timestamped[T any] struct {
	at time.Time
	v  T
}

// fetchSubject drains subject's full history from the shared stream through
// a non-durable consumer — its read position exists only for this one pass,
// never persisted, so it never competes with the beat's own durable
// consumer on the same subject. A message that fails to decode is Acked and
// dropped rather than failing the rebuild: poison on the wire is a defect
// in whatever published it, not a reason to refuse every other beat's
// history.
func fetchSubject[T any](ctx context.Context, js jetstream.JetStream, subject string) ([]timestamped[T], error) {
	// No Durable name: this consumer's cursor only needs to survive one
	// startup pass, not a restart — same reasoning as hiss's rebuildHolds.
	cons, err := js.CreateConsumer(ctx, broker.StreamName, jetstream.ConsumerConfig{
		FilterSubject:     subject,
		AckPolicy:         jetstream.AckExplicitPolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch %s: create consumer: %w", subject, err)
	}

	var out []timestamped[T]
	for {
		batch, err := cons.FetchNoWait(256)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: fetch: %w", subject, err)
		}

		var n int
		for msg := range batch.Messages() {
			var v T
			if err := wire.Unmarshal(msg.Data(), &v); err != nil {
				_ = msg.Ack() // poison would already be on the .dlq from the first pass
				continue
			}
			meta, err := msg.Metadata()
			if err != nil {
				_ = msg.Ack()
				continue
			}
			out = append(out, timestamped[T]{at: meta.Timestamp, v: v})
			_ = msg.Ack()
			n++
		}
		if err := batch.Error(); err != nil {
			return nil, fmt.Errorf("fetch %s: batch: %w", subject, err)
		}
		if n == 0 {
			break // caught up
		}
	}
	return out, nil
}
