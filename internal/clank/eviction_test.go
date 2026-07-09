package clank_test

import (
	"context"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

func TestMemProposalLog_Eviction(t *testing.T) {
	tests := map[string]struct {
		seedStatus   *proposal.Status
		seedAge      time.Duration
		wantLenAfter int
	}{
		"Record given a stale closed set evicts it": {
			seedStatus:   &proposal.Status{Phase: proposal.PhaseClosed},
			seedAge:      25 * time.Hour,
			wantLenAfter: 1, // Only the new record survives
		},
		"Record given a stale open set keeps it": { // THE A3 GUARD
			seedStatus:   &proposal.Status{Phase: proposal.PhaseProposed},
			seedAge:      25 * time.Hour,
			wantLenAfter: 2, // Both the old open set and the new record survive
		},
		"Record given a fresh closed set keeps it": {
			seedStatus:   &proposal.Status{Phase: proposal.PhaseClosed},
			seedAge:      1 * time.Hour,
			wantLenAfter: 2, // Both survive because it hasn't hit retention
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			l := clank.NewMemProposalLog()

			// 1. Arrange: Seed the stale record
			ps := proposal.Set{Status: tc.seedStatus}
			l.SeedForTest(ps, tc.seedAge)

			// 2. Act: Record a new entry to trigger the pruning
			err := l.Record(context.Background(), proposal.Set{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// 3. Assert: Verify the eviction logic
			gotLen := l.LenForTest()
			if gotLen != tc.wantLenAfter {
				t.Errorf("expected length %d after Record, got %d", tc.wantLenAfter, gotLen)
			}
		})
	}
}

func TestCaseBase_RingEviction(t *testing.T) {
	// ACE: Append given a full ring evicts the oldest case
	cb := clank.NewCaseBase()

	// Seed exactly maxCases (10,000)
	cases := make([]clank.Case, 10000)
	cases[0] = clank.Case{Fingerprint: "oldest"}
	cb.SetCasesForTest(cases)

	// Action
	err := cb.Append(clank.Case{
		Fingerprint: "newest",
		OutcomeRef:  "ref",
		DecisionRef: "ref",
		Result:      "success",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expectation
	if cb.Len() != 10000 {
		t.Errorf("expected length to cap at 10000, got %d", cb.Len())
	}

	// The oldest case should be pushed out; index 0 should now be empty (since cases[1] was empty)
	current := cb.Cases("oldest")
	if len(current) > 0 {
		t.Error("expected oldest case to be evicted, but it was found")
	}
}
