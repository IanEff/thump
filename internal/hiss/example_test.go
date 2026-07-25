package hiss_test

import (
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/hiss"
)

// ExampleAuthority_Evaluate shows the approval path: every minimum met, the
// computed risk inside the tier's auto-fire ceiling, and the band granted
// exactly the one requested — governance grants what was asked or it grants
// nothing, it never substitutes a band of its own.
func ExampleAuthority_Evaluate() {
	set := proposal.Set{
		Name:         "ps-dummy-001",
		SignalRef:    "fp-dummy-001",
		ServiceTier:  "tier-1",
		FailureClass: proposal.ClassDependencySaturation,
		Gate:         &proposal.GateResult{BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true},
		Proposals: []proposal.Candidate{{
			ID:              "p1",
			ContractRef:     "throttle-non-critical-paths",
			Confidence:      0.87,
			BlastTier:       proposal.BlastMed,
			ReversalPath:    &proposal.ReversalPath{Method: "restore-traffic-baseline"},
			GovernanceLevel: &proposal.GovernanceLevel{Band: string(decision.BandActReversible)},
			Rank:            1,
		}},
		Recommended: "p1",
	}
	pol := hiss.Policy{
		Version:         "v1",
		Floors:          map[string]map[proposal.FailureClass]float64{"tier-1": {proposal.ClassDependencySaturation: 0.75}},
		MaxBand:         map[string]decision.Band{"tier-1": decision.BandActReversible},
		AutoBand:        map[string]decision.Band{"tier-1": decision.BandActReversible},
		RequireReversal: true,
	}

	d := hiss.Authority{}.Evaluate(set, pol, time.Unix(1750000000, 0).UTC())

	fmt.Println(d.Verdict, d.GrantedBand)
	fmt.Println("risk:", d.RiskBand, "floor applied:", d.FloorApplied)

	// Output:
	// approved act_reversible
	// risk: act_reversible floor applied: 0.75
}

// ExampleAuthority_Evaluate_belowTheFloor shows what a thin diagnosis buys:
// an escalation naming the minimum it missed. Governance asks for a human
// here, it does not overrule the reasoner's ranking or reach for a
// second-choice action.
func ExampleAuthority_Evaluate_belowTheFloor() {
	set := proposal.Set{
		Name:         "ps-dummy-002",
		SignalRef:    "fp-dummy-002",
		ServiceTier:  "tier-1",
		FailureClass: proposal.ClassDependencySaturation,
		Gate:         &proposal.GateResult{BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true},
		Proposals: []proposal.Candidate{{
			ID:              "p1",
			ContractRef:     "throttle-non-critical-paths",
			Confidence:      0.60,
			BlastTier:       proposal.BlastMed,
			ReversalPath:    &proposal.ReversalPath{Method: "restore-traffic-baseline"},
			GovernanceLevel: &proposal.GovernanceLevel{Band: string(decision.BandActReversible)},
		}},
		Recommended: "p1",
	}
	pol := hiss.Policy{
		Version:         "v1",
		Floors:          map[string]map[proposal.FailureClass]float64{"tier-1": {proposal.ClassDependencySaturation: 0.75}},
		MaxBand:         map[string]decision.Band{"tier-1": decision.BandActReversible},
		AutoBand:        map[string]decision.Band{"tier-1": decision.BandActReversible},
		RequireReversal: true,
	}

	d := hiss.Authority{}.Evaluate(set, pol, time.Unix(1750000000, 0).UTC())

	fmt.Println(d.Verdict, d.Reasons)

	// Output: escalate [confidence_floor]
}

// ExampleAuthority_Evaluate_heldForAHuman shows the second stage: an action
// that met every minimum can still be too much latitude to fire unattended.
// Risk is computed from the reversal path and the blast tier a human
// authored — never from the confidence the model reported.
func ExampleAuthority_Evaluate_heldForAHuman() {
	set := proposal.Set{
		Name:         "ps-dummy-003",
		SignalRef:    "fp-dummy-003",
		ServiceTier:  "tier-1",
		FailureClass: proposal.ClassRedundancyDegraded,
		Gate:         &proposal.GateResult{BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true},
		Proposals: []proposal.Candidate{{
			ID:              "p1",
			ContractRef:     "accelerate-recovery",
			Confidence:      0.95,
			BlastTier:       proposal.BlastHigh,
			ReversalPath:    &proposal.ReversalPath{Method: "restore-recovery-defaults"},
			GovernanceLevel: &proposal.GovernanceLevel{Band: string(decision.BandActReversible)},
		}},
		Recommended: "p1",
	}
	pol := hiss.Policy{
		Version:         "v1",
		Floors:          map[string]map[proposal.FailureClass]float64{"tier-1": {proposal.ClassRedundancyDegraded: 0.3}},
		MaxBand:         map[string]decision.Band{"tier-1": decision.BandActReversible},
		AutoBand:        map[string]decision.Band{"tier-1": decision.BandActReversible},
		RequireReversal: true,
	}

	d := hiss.Authority{}.Evaluate(set, pol, time.Unix(1750000000, 0).UTC())

	fmt.Println(d.Verdict, d.RiskBand, d.Reasons)
	fmt.Printf("granted band: %q\n", d.GrantedBand)

	// Output:
	// hold act_disruptive [risk_ceiling]
	// granted band: ""
}

// ExampleAuthority_Evaluate_ungatedInput shows the one refusal that isn't a
// judgment call: a set whose evidence gate never passed is rejected without
// being weighed, because an evidence gap upstream is not governance's to
// rule on.
func ExampleAuthority_Evaluate_ungatedInput() {
	set := proposal.Set{
		Name:         "ps-dummy-004",
		SignalRef:    "fp-dummy-004",
		ServiceTier:  "tier-1",
		FailureClass: proposal.ClassDependencySaturation,
		Gate:         &proposal.GateResult{BudgetOK: true, DedupeOK: true, EvidenceOK: false, Passed: false, Reason: "evidence"},
		Proposals: []proposal.Candidate{{
			ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.99,
			BlastTier:    proposal.BlastLow,
			ReversalPath: &proposal.ReversalPath{Method: "restore-traffic-baseline"},
		}},
		Recommended: "p1",
	}
	pol := hiss.Policy{Version: "v1", RequireReversal: true}

	d := hiss.Authority{}.Evaluate(set, pol, time.Unix(1750000000, 0).UTC())

	fmt.Println(d.Verdict, d.Reasons)

	// Output: rejected [ungated_input]
}

// ExampleAuthority_Evaluate_absenceIsNotPrivilege shows how a candidate that
// asked for no band at all is read: as the lowest one, so forgetting to
// state an authority request can never be the way to get a large one.
func ExampleAuthority_Evaluate_absenceIsNotPrivilege() {
	set := proposal.Set{
		Name:         "ps-dummy-005",
		SignalRef:    "fp-dummy-005",
		ServiceTier:  "tier-1",
		FailureClass: proposal.ClassDependencySaturation,
		Gate:         &proposal.GateResult{BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true},
		Proposals: []proposal.Candidate{{
			ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87,
			BlastTier:    proposal.BlastMed,
			ReversalPath: &proposal.ReversalPath{Method: "restore-traffic-baseline"},
			// GovernanceLevel deliberately absent — no band was requested.
		}},
		Recommended: "p1",
	}
	pol := hiss.Policy{
		Version:         "v1",
		Floors:          map[string]map[proposal.FailureClass]float64{"tier-1": {proposal.ClassDependencySaturation: 0.75}},
		MaxBand:         map[string]decision.Band{"tier-1": decision.BandActDisruptive},
		AutoBand:        map[string]decision.Band{"tier-1": decision.BandActReversible},
		RequireReversal: true,
	}

	d := hiss.Authority{}.Evaluate(set, pol, time.Unix(1750000000, 0).UTC())

	fmt.Println(d.RequestedBand, d.GrantedBand)

	// Output: observe observe
}

// ExampleRiskBand shows risk computed from authored facts alone — whether
// the action carries an undo, and the blast tier a human wrote on it. An
// action nobody can reverse wants a human whatever its blast radius, and a
// reversible one that reaches wide still does.
func ExampleRiskBand() {
	fmt.Println(hiss.RiskBand(true, proposal.BlastLow))
	fmt.Println(hiss.RiskBand(true, proposal.BlastMed))
	fmt.Println(hiss.RiskBand(true, proposal.BlastHigh))
	fmt.Println(hiss.RiskBand(false, proposal.BlastLow))

	// Output:
	// act_reversible
	// act_reversible
	// act_disruptive
	// act_disruptive
}
