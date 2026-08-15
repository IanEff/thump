package rca_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/rca"
	"github.com/ianeff/thump/internal/reason"
)

// TestRunCase_AnInTopologyChangeEventRaisesComputedConfidenceAboveHoldingNone
// pins the harness wiring PR 5 fixes: newHarness used to build every Intake
// from a topology/change source that always returned the zero value, so
// LikelihoodOK could never be true and the causal term could never fire in
// task rca no matter what a row's Case described. This fails today because
// Case carries no Topology/Change fields for the harness to read at all.
func TestRunCase_AnInTopologyChangeEventRaisesComputedConfidenceAboveHoldingNone(t *testing.T) {
	t.Parallel()

	base := rca.Case{
		Name:            "a real OSD pod-failure",
		Fixture:         "node-death.yaml",
		WantDisposition: "propose",
		Evidence:        map[string]string{"pgs_degraded": "70"},
	}

	withChange := base
	withChange.Topology = proposal.TopologySnapshot{
		Upstream: []proposal.NodeState{{Name: "ceph-osd", State: "degraded", TrafficShare: 0.5}},
	}
	withChange.Change = proposal.ChangeSnapshot{
		Events: []proposal.ChangeEvent{{ID: "c1", Type: "deploy", Target: "ceph-osd", Age: 5 * time.Minute}},
	}

	run := func(t *testing.T, c rca.Case) rca.Row {
		t.Helper()
		row, err := rca.RunCase(t.Context(), c, scriptedModel(t), clank.DefaultScoringWeights(), t.TempDir(), "thump-test")
		if err != nil {
			t.Fatal(err)
		}
		return row
	}

	withoutRow := run(t, base)
	withRow := run(t, withChange)

	if withRow.Computed <= withoutRow.Computed {
		t.Errorf("a run whose change event resolved into the signal's topology scored %v, no better than the same run holding no change data at all (%v)",
			withRow.Computed, withoutRow.Computed)
	}
}

// scriptedModel replays a fixed two-turn script — a metrics lookup, then a
// propose citing it — deterministically, independent of ANTHROPIC_API_KEY.
func scriptedModel(t *testing.T) reason.Model {
	t.Helper()

	set := proposal.Set{
		FailureClass: proposal.ClassRedundancyDegraded,
		Proposals: []proposal.Candidate{{
			ID:          "p1",
			ContractRef: "hold-rebalance",
			Confidence:  1,
			Citations:   []string{"pgs_degraded"},
		}},
	}
	args, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}

	return &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"pgs_degraded"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: args}}},
	}}
}

type fakeModel struct {
	script []reason.Completion
	i      int
}

func (m *fakeModel) Complete(context.Context, []reason.Message, []reason.ToolSpec) (reason.Completion, error) {
	if m.i >= len(m.script) {
		return reason.Completion{}, nil
	}
	c := m.script[m.i]
	m.i++
	return c, nil
}
