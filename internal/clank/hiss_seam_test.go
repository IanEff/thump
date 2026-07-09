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

func TestSeam_ClankDeliveryGovernsToAnApprovedDecision(t *testing.T) {
	t.Parallel()
	// scripted model: step 1 investigate (live evidence clears the gate),
	// step 2 propose a catalogued, REVERSIBLE candidate (the seam trap, dodged).
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation, // in newTestEngine's catalog
			Hypotheses:   []proposal.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87,
				ReversalPath: &proposal.ReversalPath{ // without this, Claim 5 vetoes the seam
					Method: "unthrottle", Watching: "latency_p99", Trigger: "slo_recovery",
				},
			}},
		})}}},
	}}

	eng, sink := newTestEngine(model) // the EXACT engine the clank tests use
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal("clank leg of the seam errored:", err)
	}
	if len(sink.Delivered) != 1 {
		t.Fatalf("seam precondition: want exactly 1 delivered set, got %d", len(sink.Delivered))
	}

	// the hiss leg: the delivered set IS the input (proposal.Set is a
	// type alias for proposal.ProposalSet after Wave 1 — no conversion).
	var auth hiss.Authority
	dec := auth.Evaluate(sink.Delivered[0], seamPolicy(), time.Unix(1000, 0))

	if err := dec.Auditable(); err != nil {
		t.Error("every decision crossing the seam must be auditable:", err)
	}
	if diff := cmp.Diff(decision.VerdictApproved, dec.Verdict); diff != "" {
		t.Errorf("the three-beat happy line should end in approval (-want +got)\n%s\nreasons: %v", diff, dec.Reasons)
	}
	// the fingerprint survived detection → proposal → decision, untouched:
	if diff := cmp.Diff(sigBurnAccel().Fingerprint, dec.SignalRef); diff != "" {
		t.Error("fingerprint didn't survive the seam (-want +got)", diff)
	}
}

// seamPolicy is spelled out inline — hiss's calmPolicy() lives in package
// hiss_test and can't be imported here (same lesson as W10c's seamSource).
func seamPolicy() hiss.Policy {
	return hiss.Policy{
		Version: "seam-v1",
		Floors: map[string]map[proposal.FailureClass]float64{
			"tier-1": {proposal.ClassDependencySaturation: 0.75}, // 0.87 clears it
		},
		MaxBand:         map[string]decision.Band{"tier-1": decision.BandActReversible},
		RequireReversal: true, // prod posture, even in the seam test
	}
}
