package clank_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

// TestPropose_LogsToolCallWithStepAndFingerprint pins the E4 fix for Ian's
// "llm_complete says nothing" complaint: beat.Stage's generic
// {"msg":"llm_complete","duration_ms":...} line carries no run identity and
// no tool name, so a live incident's dispatched-tool history was only
// readable by pulling the S3 transcript. Every tool call the loop dispatches
// — including the terminal "propose"/"insufficient" calls, not just
// evidence-gathering ones — must log its own "tool_call" line carrying
// run_id, fingerprint, step, and the tool name, so the same history reads
// straight off kubectl logs.
func TestPropose_LogsToolCallWithStepAndFingerprint(t *testing.T) {
	getLogs := captureLog(t)
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}
	e, _ := newTestEngine(model)

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	var calls []map[string]any
	for _, l := range getLogs() {
		if l["msg"] == "tool_call" {
			calls = append(calls, l)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("want exactly one %q line per dispatched tool call, got %d: %+v", "tool_call", len(calls), calls)
	}

	if diff := cmp.Diff("fp-checkout-latency-001", calls[0]["fingerprint"]); diff != "" {
		t.Error("tool_call line missing/wrong fingerprint (-want +got)\n", diff)
	}
	if s, _ := calls[0]["run_id"].(string); s == "" {
		t.Errorf("tool_call line missing run_id: %+v", calls[0])
	}
	if diff := cmp.Diff(float64(0), calls[0]["step"]); diff != "" {
		t.Error("first tool_call must carry step=0 (-want +got)\n", diff)
	}
	if diff := cmp.Diff("metrics", calls[0]["tool"]); diff != "" {
		t.Error("first tool_call must name the evidence tool actually dispatched (-want +got)\n", diff)
	}

	if diff := cmp.Diff(float64(1), calls[1]["step"]); diff != "" {
		t.Error("second tool_call must carry step=1 (-want +got)\n", diff)
	}
	if diff := cmp.Diff("propose", calls[1]["tool"]); diff != "" {
		t.Error("second tool_call must name the terminal propose call, not just evidence tools (-want +got)\n", diff)
	}
}
