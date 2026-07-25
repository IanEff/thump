package thump_test

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/thump"
)

// disarmedSwitch is off, the state live actuation sits in until an operator
// arms it — the answer to "do we trust anything live right now", never to
// "is this particular order allowed".
type disarmedSwitch struct{}

func (disarmedSwitch) Armed(context.Context) bool { return false }

// exampleCatalog is the authored action the Examples below render against —
// one contract, its scope defaults, and the undo authored beside it.
func exampleCatalog() *contract.StaticCatalog {
	return contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		BlastTier:                proposal.BlastMed,
		Action: contract.ActionSpec{
			Description:     "Shed synthetic load from the saturated path.",
			ScopeParameters: map[string]contract.Range{"throttle_replicas": {Min: 0, Max: 5, Default: 2}},
		},
		Reversal: contract.Reversal{Method: "restore-traffic-baseline", Fallback: "page-oncall"},
	}})
}

// exampleGoverned is one approval sealed against the set it judged — the
// decision and the set have to describe each other or rendering refuses.
func exampleGoverned() decision.Governed {
	return decision.Governed{
		Decision: decision.Decision{
			ID:            "dec:fp-dummy-001:1750000000",
			ProposalRef:   "ps-dummy-001",
			SignalRef:     "fp-dummy-001",
			CandidateRef:  "p1",
			Verdict:       decision.VerdictApproved,
			GrantedBand:   decision.BandActReversible,
			PolicyVersion: "v1",
			EvaluatedAt:   time.Unix(1750000000, 0).UTC(),
		},
		Set: proposal.Set{
			Name:      "ps-dummy-001",
			SignalRef: "fp-dummy-001",
			Proposals: []proposal.Candidate{{
				ID:           "p1",
				ContractRef:  "throttle-non-critical-paths",
				Confidence:   0.87,
				ReversalPath: &proposal.ReversalPath{Method: "restore-traffic-baseline", Watching: "rgw_get_put_latency_ms"},
			}},
			Recommended: "p1",
		},
	}
}

// ExampleActuator_Render shows that acting invents no numbers: the replica
// count in the rendered order is the default the action's author wrote in
// the catalog, and the fallback comes from the same place. Every field
// traces back to the decision, the recommended candidate, or the contract.
func ExampleActuator_Render() {
	o, err := thump.Actuator{}.Render(exampleGoverned(), exampleCatalog(), time.Unix(1750000000, 0).UTC())
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(o.ContractRef, o.Parameters)
	fmt.Println("undo:", o.Reversal.Method, "fallback:", o.Reversal.Fallback)

	// Output:
	// throttle-non-critical-paths map[throttle_replicas:2]
	// undo: restore-traffic-baseline fallback: page-oncall
}

// ExampleActuator_Render_outsideCatalog shows the boundary that bounds blast
// radius: a granted candidate naming an action the catalog doesn't list is
// refused outright rather than rendered on the strength of the approval.
func ExampleActuator_Render_outsideCatalog() {
	g := exampleGoverned()
	g.Set.Proposals[0].ContractRef = "delete-the-namespace"

	_, err := thump.Actuator{}.Render(g, exampleCatalog(), time.Unix(1750000000, 0).UTC())

	fmt.Println(err)

	// Output: thump: granted contract not in catalog: "delete-the-namespace"
}

// ExampleActuator_Render_notAnApproval shows that only one verdict is
// actionable: a held or escalated decision is refused here, because
// converting either into permission is governance's job and rendering has
// no standing to do it.
func ExampleActuator_Render_notAnApproval() {
	g := exampleGoverned()
	g.Decision.Verdict = decision.VerdictHold
	g.Decision.Reasons = []string{decision.ReasonRiskCeiling}

	_, err := thump.Actuator{}.Render(g, exampleCatalog(), time.Unix(1750000000, 0).UTC())

	fmt.Println(err)

	// Output: thump: decision is not an approval: verdict "hold"
}

// ExampleGatedExecutor_Execute shows a disarmed kill switch producing a
// recorded refusal instead of a quiet skip: the outcome says blocked, which
// is not a failure, and it stands up as an audit record with no error text.
func ExampleGatedExecutor_Execute() {
	exec := thump.GatedExecutor{Inner: thump.DryRun{}, Switch: disarmedSwitch{}}
	order := thump.Order{
		ID:          "ord:fp-dummy-001:1750000000",
		DecisionRef: "dec:fp-dummy-001:1750000000",
		SignalRef:   "fp-dummy-001",
		ContractRef: "throttle-non-critical-paths",
	}

	out := exec.Execute(context.Background(), order, time.Unix(1750000000, 0).UTC())

	fmt.Println(out.Mode, out.Result)
	fmt.Println(out.Auditable())

	// Output:
	// live blocked
	// <nil>
}

// ExampleGatedExecutor_Execute_reversal shows the one exemption the switch
// makes: an undo reaches the executor even while live action is disarmed,
// because stranding infrastructure half-changed is worse than letting one
// already-approved undo finish. The wrapped executor here is the dry-run
// one, so what it reports is a rendering rather than a live result.
func ExampleGatedExecutor_Execute_reversal() {
	exec := thump.GatedExecutor{Inner: thump.DryRun{}, Switch: disarmedSwitch{}}
	undo := thump.Order{
		ID:          "ord:fp-dummy-001:1750000001",
		Kind:        thump.OrderReversal,
		DecisionRef: "dec:fp-dummy-001:1750000000",
		SignalRef:   "fp-dummy-001",
		ContractRef: "throttle-non-critical-paths",
	}

	out := exec.Execute(context.Background(), undo, time.Unix(1750000001, 0).UTC())

	fmt.Println(out.Mode, out.Result)

	// Output: dry_run rendered
}
