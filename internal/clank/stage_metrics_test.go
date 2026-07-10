package clank_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/clank"
)

// TestPropose_RecordsStageDurationForEveryStage pins B3: once a
// beat.StageRecorder is wired onto the Engine, one Propose run observes a
// duration sample for each of the four reason-loop stages — the same
// boundaries B1's spans already stand on. A stage silently missing here
// means a beat's dashboard would show a gap it can't explain from Tempo
// alone.
func TestPropose_RecordsStageDurationForEveryStage(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}

	e, _ := newTestEngine(model)
	reg := prometheus.NewRegistry()
	e.Stages = beat.NewStageRecorder(reg)

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	got := map[string]uint64{}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "stage_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lbl := range m.GetLabel() {
				if lbl.GetName() == "stage" {
					got[lbl.GetValue()] = m.GetHistogram().GetSampleCount()
				}
			}
		}
	}

	// llm_complete fires once per reason-loop turn — this fake model's script
	// runs two turns (a tool call, then propose) before the loop exits, so it
	// alone wants 2; every other stage runs exactly once per Propose call.
	want := map[string]uint64{"assemble_sao": 1, "llm_complete": 2, "causal_score": 1, "gate_eval": 1}
	for stage, wantCount := range want {
		if got[stage] != wantCount {
			t.Errorf("stage_duration_seconds{stage=%q} sample count = %d, want %d", stage, got[stage], wantCount)
		}
	}
}
