package clank_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

// recordingStore captures every RunID Checkpoint sees, across calls — just
// enough to prove Propose mints a fresh key per invocation instead of
// reusing the bare fingerprint, which would let one run's checkpoints
// clobber another's at the same step number.
type recordingStore struct {
	clank.Store
	runIDs []string
}

func (s *recordingStore) Checkpoint(ctx context.Context, t clank.Turn) error {
	s.runIDs = append(s.runIDs, t.RunID)
	return s.Store.Checkpoint(ctx, t)
}

func TestPropose_TwoRunsOfTheSameFingerprintCheckpointUnderDifferentRunIDs(t *testing.T) {
	t.Parallel()
	store := &recordingStore{Store: clank.NewMemStore()}
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}
	e, _ := newTestEngine(model)
	e.Store = store

	sig := sigBurnAccel()
	if _, err := e.Propose(context.Background(), sig); err != nil {
		t.Fatalf("first Propose errored: %v", err)
	}
	model.i = 0 // replay the same script for a second, independent run
	if _, err := e.Propose(context.Background(), sig); err != nil {
		t.Fatalf("second Propose errored: %v", err)
	}

	if len(store.runIDs) != 2 {
		t.Fatalf("want 2 checkpointed turns (one per run), got %d: %v", len(store.runIDs), store.runIDs)
	}
	if store.runIDs[0] == store.runIDs[1] {
		t.Errorf("two Propose calls for the same fingerprint must not share a checkpoint RunID, both got %q — a redelivery or retry would silently clobber the first run's transcript", store.runIDs[0])
	}
	for _, id := range store.runIDs {
		if !strings.HasPrefix(id, sig.Fingerprint+"/") {
			t.Errorf("RunID %q must stay prefixed by the fingerprint %q, so a run's checkpoints are still listable by signal", id, sig.Fingerprint)
		}
	}
}

// TestPropose_FinishesTheRunSoStorePendingComesBackEmpty pins that every
// Propose exit — proposed, declined, and budget-exhausted alike — calls
// Store.Finish, not just Checkpoint. TestStore_FinishRemovesARunFromPending
// (store_test.go) already pins Finish itself; this pins that Propose
// actually drives it, on every path out.
func TestPropose_FinishesTheRunSoStorePendingComesBackEmpty(t *testing.T) {
	t.Parallel()
	metricsCall := clank.Completion{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}}

	for _, tc := range []struct {
		name   string
		script []clank.Completion
	}{
		{"proposed", []clank.Completion{
			{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
				FailureClass: proposal.ClassDependencySaturation,
				Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
				Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
			})}}},
		}},
		{"declined", nil}, // empty script -> Complete returns no tool calls on the first turn
		{"budget_exhausted", []clank.Completion{
			metricsCall, metricsCall, metricsCall, metricsCall,
			metricsCall, metricsCall, metricsCall, metricsCall, // MaxSteps=8, never proposes
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := clank.NewMemStore()
			e, _ := newTestEngine(&fakeModel{script: tc.script})
			e.Store = store

			if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
				t.Fatalf("Propose errored: %v", err)
			}
			pending, err := store.Pending(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				t.Errorf("want Store.Pending empty once Propose returns, got %d pending turn(s): %+v", len(pending), pending)
			}
		})
	}
}
