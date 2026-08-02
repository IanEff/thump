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
	var events []replayEvent
	if err := broker.DrainSubject(ctx, js, "thump.proposals", "fetch thump.proposals", func(at time.Time, v proposal.Set) {
		events = append(events, replayEvent{at: at, proposal: &v})
	}); err != nil {
		return nil, fmt.Errorf("clank: rebuild ledger: %w", err)
	}
	if err := broker.DrainSubject(ctx, js, "thump.outcomes", "fetch thump.outcomes", func(at time.Time, v outcome.Outcome) {
		events = append(events, replayEvent{at: at, outcome: &v})
	}); err != nil {
		return nil, fmt.Errorf("clank: rebuild ledger: %w", err)
	}
	if err := broker.DrainSubject(ctx, js, "thump.declines", "fetch thump.declines", func(at time.Time, v decision.Decision) {
		events = append(events, replayEvent{at: at, decline: &v})
	}); err != nil {
		return nil, fmt.Errorf("clank: rebuild ledger: %w", err)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })

	ledger := NewMemProposalLog()
	ledger.LedgerRetention = retention
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
