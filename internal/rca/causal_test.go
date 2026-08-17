package rca_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/rca"
	"github.com/ianeff/thump/internal/reason"
)

// TestRunCase_AnInTopologyChangeEventRaisesComputedConfidenceAboveHoldingNone
// asserts that an in-topology change event raises computed confidence via the
// causal term above holding no change data at all.
func TestRunCase_AnInTopologyChangeEventRaisesComputedConfidenceAboveHoldingNone(t *testing.T) {
	t.Parallel()

	base := rca.Case{
		Name:            "a real cartFailure flag flip",
		Rig:             "dev",
		Fixture:         "disable-cart-failure.yaml",
		WantDisposition: "propose",
		WantContractRef: "disable-cart-failure",
		WantClass:       proposal.ClassServiceFailure,
		MustCite:        []string{"cart_error_ratio"},
		Evidence:        map[string]string{"cart_error_ratio": "0.4737"},
	}

	det, err := rca.LoadDetectionForTest(base.Fixture)
	if err != nil {
		t.Fatal(err)
	}

	withChange := base
	withChange.FaultObjects = []runtime.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       "otel-demo",
				Name:            "flagd-config",
				ResourceVersion: "100",
				ManagedFields: []metav1.ManagedFieldsEntry{
					{
						Manager: "kubectl-edit",
						Time:    &metav1.Time{Time: det.DetectedAt.Add(-2 * time.Minute)},
					},
				},
			},
		},
	}

	run := func(t *testing.T, c rca.Case) rca.Row {
		t.Helper()
		row, err := rca.RunCase(t.Context(), c, cartScriptedModel(t), clank.DefaultScoringWeights(), t.TempDir(), c.Rig)
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

// TestRigChange_AChangeOutsideTheBurningServicesPathStaysOutOfTopology pins
// that widening what the graph observes never widens what counts: findNode
// matches an event's Target against node names by exact equality
// (internal/clank/causal.go:120-129) and scoreConfidences admits only
// InTopology scores (confidence.go:50-57), so an edit to a service the signal
// does not depend on must score InTopology false and leave computed confidence
// byte-identical to a run with no change events at all.
func TestRigChange_AChangeOutsideTheBurningServicesPathStaysOutOfTopology(t *testing.T) {
	t.Parallel()

	base := rca.Case{
		Name:            "a real cartFailure flag flip",
		Rig:             "dev",
		Fixture:         "disable-cart-failure.yaml",
		WantDisposition: "propose",
		WantContractRef: "disable-cart-failure",
		WantClass:       proposal.ClassServiceFailure,
		MustCite:        []string{"cart_error_ratio"},
		Evidence:        map[string]string{"cart_error_ratio": "0.4737"},
	}

	det, err := rca.LoadDetectionForTest(base.Fixture)
	if err != nil {
		t.Fatal(err)
	}

	// Unrelated fault: acme-fault-flag in namespace acme (resolves to acme-db, not in cart's topology)
	unrelatedCase := base
	unrelatedCase.FaultObjects = []runtime.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       "acme",
				Name:            "acme-fault-flag",
				ResourceVersion: "200",
				ManagedFields: []metav1.ManagedFieldsEntry{
					{
						Manager: "kubectl-edit",
						Time:    &metav1.Time{Time: det.DetectedAt.Add(-2 * time.Minute)},
					},
				},
			},
		},
	}

	// In-topology fault: flagd-config in namespace otel-demo (resolves to flagd, which cart depends on)
	inTopologyCase := base
	inTopologyCase.FaultObjects = []runtime.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       "otel-demo",
				Name:            "flagd-config",
				ResourceVersion: "100",
				ManagedFields: []metav1.ManagedFieldsEntry{
					{
						Manager: "kubectl-edit",
						Time:    &metav1.Time{Time: det.DetectedAt.Add(-2 * time.Minute)},
					},
				},
			},
		},
	}

	run := func(t *testing.T, c rca.Case) rca.Row {
		t.Helper()
		row, err := rca.RunCase(t.Context(), c, cartScriptedModel(t), clank.DefaultScoringWeights(), t.TempDir(), c.Rig)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}

	baseRow := run(t, base)
	unrelatedRow := run(t, unrelatedCase)
	inTopologyRow := run(t, inTopologyCase)

	if diff := cmp.Diff(baseRow.Computed, unrelatedRow.Computed); diff != "" {
		t.Errorf("unrelated change event modified computed confidence (-base +unrelated):\n%s", diff)
	}

	if inTopologyRow.Computed <= baseRow.Computed {
		t.Errorf("in-topology change event computed confidence (%v) did not exceed base (%v)",
			inTopologyRow.Computed, baseRow.Computed)
	}
}

// cartScriptedModel replays a fixed two-turn script for cart-failure — a metrics
// lookup, then a propose citing it — deterministically, independent of ANTHROPIC_API_KEY.
func cartScriptedModel(t *testing.T) reason.Model {
	t.Helper()

	set := proposal.Set{
		FailureClass: proposal.ClassServiceFailure,
		Proposals: []proposal.Candidate{{
			ID:          "p1",
			ContractRef: "disable-cart-failure",
			Confidence:  1,
			Citations:   []string{"cart_error_ratio"},
		}},
	}
	args, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}

	return &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"cart_error_ratio"}`)}}},
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
