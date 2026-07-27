//go:build eval
// +build eval

package integration_test

import (
	"testing"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
)

// TestEngine_GoldenPathAgainstRealModel_SignalToDeliveredProposalSet is the
// hermetic golden path's live counterpart — same wiring, the real Anthropic
// model in place of a scripted one. Only this version can prove the model's
// own judgment (which failure class it names, which citations it grounds a
// proposal in) rather than replaying a canned answer.
func TestEngine_GoldenPathAgainstRealModel_SignalToDeliveredProposalSet(t *testing.T) {
	metrics := &recordingTool{
		spec: clank.ToolSpec{Name: "metrics", Description: "read-only telemetry query for a service's live metrics"},
		ref: proposal.EvidenceRef{
			Tool: "metrics", Query: "payments-db-cpu",
			Summary: "payments-db CPU pinned at 99%, connection pool exhausted",
			Ref:     "metrics://payments-db/cpu", Live: true,
		},
	}
	// Broadly applicable on purpose: this test exercises the LOOP, not Haiku's
	// taste in failure-class labels. Whatever class it picks, the action stays
	// in-catalog, so the test fails only for real wiring reasons.
	catalog := contract.NewStaticCatalog([]contract.ActionContract{{
		Name: "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{
			proposal.ClassDependencySaturation, proposal.ClassResourceExhaustion,
			proposal.ClassTrafficShift, proposal.ClassUnknown,
		},
		ApplicableTiers: []string{"tier-1"},
	}})

	e, sink := newEngine(t, newModel(t), metrics, catalog)
	set, err := e.Propose(callCtx(t), goldenSignal())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	// The loop investigated before it proposed — the gate's live-evidence floor
	// (anyLive) depends on it, and so does honest reasoning.
	if metrics.calls == 0 {
		t.Error("model proposed without calling the telemetry tool; the loop didn't investigate")
	}
	if set.Status.Phase != "proposed" {
		t.Fatalf("golden path should reach phase \"proposed\"; got %q (gate %+v)", set.Status.Phase, set.Gate)
	}
	if set.Gate == nil || !set.Gate.Passed {
		t.Errorf("a grounded, deduped, in-budget set must pass the gate: %+v", set.Gate)
	}
	if len(set.Proposals) == 0 {
		t.Fatal("a passed set must carry at least one proposal")
	}
	// Autonomy boundary, behavioural, against the real model — the hermetic
	// twin can only ever emit what it's scripted to, so this is the one place
	// in the suite that actually asks whether the model would go off-catalog.
	for _, c := range set.Proposals {
		if c.ContractRef != "throttle-non-critical-paths" {
			t.Errorf("proposed an action outside the catalog: %q", c.ContractRef)
		}
	}
	if set.Recommended != set.Proposals[0].ID {
		t.Errorf("recommended must be the rank-1 proposal: rec=%q rank1=%q", set.Recommended, set.Proposals[0].ID)
	}
	if set.SAOSnapshot == nil || set.SAOSnapshot.Version == 0 {
		t.Error("the SAO the loop reasoned over must be frozen onto the set")
	}
	if len(sink.Delivered) != 1 {
		t.Errorf("a passed set is delivered exactly once; delivered %d", len(sink.Delivered))
	}
}

// TestEngine_ThinEvidenceAgainstRealModel_YieldsNoActionAndDeliversNothing is
// the hermetic decline test's live counterpart — only the real model's
// judgment can prove "all nominal" evidence doesn't get talked into a
// proposal; a scripted Completion can only prove the engine's plumbing
// handles a decline correctly, which the hermetic twin already covers.
func TestEngine_ThinEvidenceAgainstRealModel_YieldsNoActionAndDeliversNothing(t *testing.T) {
	metrics := &recordingTool{
		spec: clank.ToolSpec{Name: "metrics", Description: "read-only telemetry query for a service's live metrics"},
		ref:  proposal.EvidenceRef{Tool: "metrics", Summary: "all services nominal; no anomaly on payments-db", Ref: "metrics://payments-db/cpu", Live: true},
	}
	catalog := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
	}})

	e, sink := newEngine(t, newModel(t), metrics, catalog)
	set, err := e.Propose(callCtx(t), goldenSignal())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if set.Status.Phase == "proposed" {
		t.Errorf("evidence saying \"all nominal\" should not reach a proposal: %+v", set)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a non-proposed set must deliver nothing; delivered %d", len(sink.Delivered))
	}
}
