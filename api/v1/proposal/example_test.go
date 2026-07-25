package proposal_test

import (
	"fmt"

	"github.com/ianeff/thump/api/v1/proposal"
)

// ExampleSet_ContractRefFor shows why the whole ranked set is the audit
// unit rather than the winner alone: every candidate the reasoner weighed
// stays on the record, and the recommendation is a lookup into that record.
func ExampleSet_ContractRefFor() {
	set := proposal.Set{
		Name:         "ps-dummy-001",
		SignalRef:    "fp-dummy-001",
		FailureClass: proposal.ClassServiceFailure,
		Proposals: []proposal.Candidate{
			{ID: "p1", ContractRef: "disable-cart-failure", Confidence: 0.88, Rank: 1},
			{ID: "p2", ContractRef: "restart-cart-pod", Confidence: 0.71, Rank: 2},
		},
		Recommended: "p1",
	}

	fmt.Println(set.ContractRefFor(set.Recommended))
	fmt.Println(set.ContractRefFor("p2"))
	fmt.Printf("%q\n", set.ContractRefFor("p-never-proposed"))

	// Output:
	// disable-cart-failure
	// restart-cart-pod
	// ""
}

// ExampleSet_ConfidenceFor shows the reasoner's half of the two-number
// split: this is how sure the diagnosis is, not how trustworthy the input
// was — that one is the detector's, and it rides a different object.
func ExampleSet_ConfidenceFor() {
	set := proposal.Set{
		Name:      "ps-dummy-001",
		SignalRef: "fp-dummy-001",
		Proposals: []proposal.Candidate{
			{ID: "p1", ContractRef: "disable-cart-failure", Confidence: 0.88, Rank: 1},
		},
		Recommended: "p1",
	}

	fmt.Println(set.ConfidenceFor("p1"))
	fmt.Println(set.ConfidenceFor("p-never-proposed"))

	// Output:
	// 0.88
	// 0
}

// ExampleGateResult shows the readiness gate as a conjunction of minimums,
// never an average: two dimensions cleared and one failed is a failed gate,
// and Reason names the one that vetoed.
func ExampleGateResult() {
	// What clank computed for a set whose only citations were historical —
	// no live telemetry backed the diagnosis.
	g := proposal.GateResult{
		BudgetOK:   true,
		DedupeOK:   true,
		EvidenceOK: false,
		Passed:     false,
		Reason:     "evidence",
	}

	fmt.Println(g.Passed, g.Reason)
	fmt.Println(g.BudgetOK && g.DedupeOK && g.EvidenceOK)

	// Output:
	// false evidence
	// false
}
