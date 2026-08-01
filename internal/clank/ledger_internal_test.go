package clank

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
)

func TestSeedAt_StampsTheGivenTimeNotNow(t *testing.T) {
	t.Parallel()
	l := NewMemProposalLog()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	l.seedAt(proposal.Set{SignalRef: "fp-1", Status: &proposal.Status{Phase: proposal.PhaseProposed}}, at)

	if diff := cmp.Diff(at, l.sets[0].at); diff != "" {
		t.Error("wrong recorded.at stamped by seedAt (-want +got)", diff)
	}
}

func TestSeedAt_OpenRespectsTheStampedTimeNotWallClock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := NewMemProposalLog()
	now := time.Now()

	l.seedAt(proposal.Set{SignalRef: "fp-1", Status: &proposal.Status{Phase: proposal.PhaseProposed}}, now.Add(-2*time.Hour))

	stale, err := l.Open(ctx, "fp-1", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("a set seeded 2h ago must not answer a 1h dedupe window: want 0, got %d", len(stale))
	}

	fresh, err := l.Open(ctx, "fp-1", now.Add(-3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 {
		t.Errorf("a set seeded 2h ago must answer a 3h dedupe window: want 1, got %d", len(fresh))
	}
}
