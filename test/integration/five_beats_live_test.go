//go:build eval

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/thump"
)

// livePolicy is the governance fixture this test is judged under. The floor is
// data handed to the gate, never logic in the reasoner — if a grounded run's
// confidence lands under it, this number is what moves, and the engine is not
// touched. 0.50 is the highest floor a single-citation run can clear: one live
// citation caps emitted confidence at 0.52 (signal 0.90 × grounding 0.70 ×
// causal likelihood 0.83), while an uncited candidate reaches only 0.22.
func livePolicy() hiss.Policy {
	return hiss.Policy{
		Version: "five-beat-live-v1",
		Floors: map[string]map[proposal.FailureClass]float64{
			"tier-1": {proposal.ClassResourceExhaustion: 0.50},
		},
		MaxBand:         map[string]decision.Band{"tier-1": decision.BandActReversible},
		RequireReversal: true,
	}
}

// liveCatalog offers exactly one reversible action, applicable broadly on
// purpose: this test exercises the whole machine, not Haiku's taste in
// failure-class labels, so whichever class it names the action stays in
// catalog and a failure means a real seam problem.
func liveCatalog() *contract.StaticCatalog {
	return contract.NewStaticCatalog([]contract.ActionContract{{
		Name: "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{
			proposal.ClassDependencySaturation, proposal.ClassResourceExhaustion,
			proposal.ClassTrafficShift, proposal.ClassUnknown,
		},
		ApplicableTiers: []string{"tier-1"},
		Action: contract.ActionSpec{
			Description:     "Throttle non-critical request paths at the ingress",
			ScopeParameters: map[string]contract.Range{"throttle_pct": {Min: 10, Max: 60, Default: 25}},
		},
		Reversal:        contract.Reversal{Method: "unthrottle", Fallback: "page-oncall"},
		SuccessCriteria: contract.SuccessCriteria{Metric: "latency_p99", Target: "p99 < 250ms", Window: 10 * time.Minute},
	}})
}

// TestGoldenPath_FiveBeatsAgainstTheRealModel is the whole machine, once, for
// real: clank reasons with the production Anthropic model, hiss governs under a
// shipped-shape policy, thump renders and dry-run executes, and click absorbs
// the outcome back into the same ledger. Everything is real except the cluster
// — the executor is DryRun and the tool is a fixture, so nothing is mutated and
// no rig is needed. It is the only test in the suite where the model's judgment
// has to survive all five beats instead of one.
func TestGoldenPath_FiveBeatsAgainstTheRealModel(t *testing.T) {
	metrics := &recordingTool{
		spec: clank.ToolSpec{Name: "metrics", Description: "read-only telemetry query for a service's live metrics"},
		ref: proposal.EvidenceRef{
			Tool: "metrics", Query: "payments-db-cpu",
			Summary: "payments-db CPU pinned at 99%, connection pool exhausted",
			Ref:     "metrics://payments-db/cpu", Live: true,
		},
	}

	// beats one and two: real detection shape, real engine, real model.
	e, sink := newEngine(t, newModel(t), metrics, liveCatalog())
	// newEngine leaves Weights unset; a zero ScoringWeights silently zeroes
	// every candidate's confidence, which then fails any floor.
	e.Weights = clank.DefaultScoringWeights()
	set, err := e.Propose(callCtx(t), goldenSignal())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if metrics.calls == 0 {
		t.Error("the model proposed without calling the telemetry tool; the loop didn't investigate")
	}
	if set.Gate == nil || !set.Gate.Passed {
		t.Fatalf("a grounded set must clear the gate: %+v (status %+v)", set.Gate, set.Status)
	}
	if len(sink.Delivered) != 1 {
		t.Fatalf("a passed set is delivered exactly once; delivered %d", len(sink.Delivered))
	}

	// beat three: govern.
	now := time.Now()
	dec := hiss.Authority{}.Evaluate(sink.Delivered[0], livePolicy(), now)
	if diff := cmp.Diff(decision.VerdictApproved, dec.Verdict); diff != "" {
		t.Fatalf("the policy must approve a grounded set (-want +got)\n%s\nreasons: %v\nconfidence %.2f vs floor %.2f",
			diff, dec.Reasons, set.ConfidenceFor(set.Recommended), dec.FloorApplied)
	}

	// beat four: render, then rehearse.
	order, err := thump.Actuator{}.Render(
		decision.Governed{Decision: dec, Set: sink.Delivered[0]}, liveCatalog(), now)
	if err != nil {
		t.Fatalf("thump refused to render an approved action: %v", err)
	}
	oc := thump.DryRun{}.Execute(context.Background(), order, now)
	if err := oc.Auditable(); err != nil {
		t.Error("every outcome crossing the seam must be auditable:", err)
	}
	if diff := cmp.Diff(outcome.ResultRendered, oc.Result); diff != "" {
		t.Error("the five-beat path must end in a rendered dry-run order (-want +got)\n", diff)
	}

	// beat five: the return edge, into the engine's own ledger.
	cases := clank.NewCaseBase()
	click := clank.Click{Ledger: e.Ledger, Cases: cases}
	if err := click.Absorb(context.Background(), oc); err != nil {
		t.Fatal("click leg errored:", err)
	}
	if diff := cmp.Diff(goldenSignal().Fingerprint, oc.SignalRef); diff != "" {
		t.Error("the fingerprint didn't survive five beats (-want +got)\n", diff)
	}
	if _, corroborated := cases.Alignment(goldenSignal().Fingerprint); corroborated {
		t.Error("the loop closed on a rehearsal — nothing may be believed off a dry run")
	}
}
