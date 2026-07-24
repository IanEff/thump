package clank_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/hiss"
)

// TestProductionWiring_ConfidenceIsTheProductOfBothBeatConstructors strengthens
// TestLoop_DeliversNonZeroConfidenceThroughProductionWiring: instead of asserting
// > 0, it reconstructs the exact product from the audit trail. A partial factor
// drop — e.g. signalConf silently 1.0 instead of rattle's real Divergence value —
// fails here and would still pass the > 0 pin.
func TestProductionWiring_ConfidenceIsTheProductOfBothBeatConstructors(t *testing.T) {
	t.Parallel()

	det := seamDetection(t)               // real rattle.Reconcile → Divergence.Confidence set, not 0
	loop := newTestLoop(t)                // real newLoop via NewLoopForTest; scripts one live citation @0.87
	loop.Engine.Intake = noChangeIntake() // isolate confidence to citation-grounding, not the fixture's incidental fake change event

	set, err := loop.Engine.Propose(context.Background(), det)
	if err != nil {
		t.Fatal(err)
	}

	// signalConf (rattle) × grounding tier for one corroborated live citation
	// (clank), capped by the model's 0.87 self-report. Reconstructed, not a literal.
	want := clank.ScoreConfidenceForTest(det.Divergence.Confidence, 1, 0.87)
	got := set.Proposals[0].Confidence

	if got <= 0 {
		t.Fatalf("zero confidence through production wiring — a dropped constructor factor:\n%+v", set.Proposals[0])
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("emitted confidence isn't the product both real constructors should carry (-want +got)\n%s", diff)
	}
}

// TestProductionWiring_FiveBeatsReachApprovedThroughRealConstructors routes the
// five-beat chain through newLoop (not newTestEngine), to the first approved
// verdict on a confidence hand-set nowhere in the wiring.
//
// Skeleton: newApprovableTestLoop below still needs its intake/tools/catalog
// wired to match seamCatalog() before this can run for real — see its TODO.
func TestProductionWiring_FiveBeatsReachApprovedThroughRealConstructors(t *testing.T) {
	t.Parallel()

	loop := newApprovableTestLoop(t) // local builder below — the delta from newTestLoop
	set, err := loop.Engine.Propose(context.Background(), seamDetection(t))
	if err != nil {
		t.Fatal("clank leg errored:", err)
	}

	var auth hiss.Authority
	dec := auth.Evaluate(set, seamPolicy(), time.Unix(1000, 0))
	if diff := cmp.Diff(decision.VerdictApproved, dec.Verdict); diff != "" {
		t.Fatalf("first approved verdict must come through real wiring (-want +got)\n%s\nreasons: %v", diff, dec.Reasons)
	}
	// thump.Actuator.Render + DryRun.Execute legs copy from the four-beat seam
	// test's thump/click legs once this skeleton is filled in.
}

// newApprovableTestLoop mirrors newTestLoop but scripts a well-corroborated,
// reversible, band-requesting proposal so hiss approves. The catalog must match
// seamCatalog()'s throttle entry (reversal + success criteria).
func newApprovableTestLoop(t *testing.T) testLoop {
	t.Helper()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87,
				Citations:       []string{`{"q":"burn"}`, `{"q":"latency_p99"}`},
				ReversalPath:    &proposal.ReversalPath{Method: "unthrottle", Watching: "latency_p99", Trigger: "slo_recovery"},
				GovernanceLevel: &proposal.GovernanceLevel{Band: string(decision.BandActReversible)},
			}},
		})}}},
	}}

	tools := map[string]clank.Tool{"metrics": metricsTool{}}
	store := clank.NewMemStore()

	l := clank.NewLoopForTest(model, tools, noChangeIntake(), seamCatalog(), t.TempDir(), t.TempDir(), store)
	return testLoop{Engine: l.Engine, ReturnEdge: l.ReturnEdge, Cases: l.Cases, OutcomeInbox: l.OutcomeInbox}
}
