package decision_test

import (
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
)

// ExampleDecision_Auditable shows what a verdict is born owing: one that
// can't name the policy it was judged under is refused, so no approval
// reaches execution that a reviewer couldn't re-check against the rules of
// the day.
func ExampleDecision_Auditable() {
	d := decision.Decision{
		ID:           "dec:fp-dummy-001:1750000000",
		ProposalRef:  "ps-dummy-001",
		SignalRef:    "fp-dummy-001",
		CandidateRef: "p1",
		Verdict:      decision.VerdictApproved,
		GrantedBand:  decision.BandActReversible,
		EvaluatedAt:  time.Unix(1750000000, 0).UTC(),
	}

	fmt.Println(d.Auditable())

	d.PolicyVersion = "v1"
	fmt.Println(d.Auditable())

	// Output:
	// decision missing policy version
	// <nil>
}

// ExampleDecision_Auditable_refusalWithNoReason shows that declining is an
// audit record too — a non-approval carrying no Reasons is refused as hard
// as a missing policy version, because a verdict nobody can explain is the
// silence this engine exists to not have.
func ExampleDecision_Auditable_refusalWithNoReason() {
	d := decision.Decision{
		ID:            "dec:fp-dummy-002:1750000000",
		SignalRef:     "fp-dummy-002",
		Verdict:       decision.VerdictEscalate,
		PolicyVersion: "v1",
		EvaluatedAt:   time.Unix(1750000000, 0).UTC(),
	}

	fmt.Println(d.Auditable())

	d.Reasons = []string{decision.ReasonConfidenceFloor}
	fmt.Println(d.Auditable())

	// Output:
	// escalate decision with no reasons is not accepted
	// <nil>
}
