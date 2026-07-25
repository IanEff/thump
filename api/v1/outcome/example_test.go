package outcome_test

import (
	"fmt"
	"strconv"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

// ExampleOutcome_Auditable shows the one thing a failed act owes: error
// text. A failure nobody wrote down reads exactly like a success later, so
// the empty string is refused rather than recorded.
func ExampleOutcome_Auditable() {
	o := outcome.Outcome{
		ID:          "out:fp-dummy-001:1750000000",
		DecisionRef: "dec:fp-dummy-001:1750000000",
		SignalRef:   "fp-dummy-001",
		ContractRef: "throttle-non-critical-paths",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultFailure,
		ExecutedAt:  time.Unix(1750000000, 0).UTC(),
	}

	fmt.Println(o.Auditable())

	o.Error = "scale rejected: deployment not found"
	fmt.Println(o.Auditable())

	// Output:
	// failure outcome with no error text is silence, not accountability
	// <nil>
}

// ExampleOutcome_Auditable_blocked shows the refusal a disarmed kill switch
// produces: blocked is a recorded decision not to act, not a failure, so it
// stands up as an audit record carrying no error text at all.
func ExampleOutcome_Auditable_blocked() {
	o := outcome.Outcome{
		ID:          "out:fp-dummy-002:1750000000",
		DecisionRef: "dec:fp-dummy-002:1750000000",
		SignalRef:   "fp-dummy-002",
		ContractRef: "throttle-non-critical-paths",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultBlocked,
		ExecutedAt:  time.Unix(1750000000, 0).UTC(),
	}

	fmt.Println(o.Auditable())

	// Output: <nil>
}

// ExampleOutcome_unmeasuredSeverity shows why ObservedSeverity is a pointer:
// nil means nobody measured it, and it has to render as its own word rather
// than as a zero sitting next to a real 0.60 looking like a clean win.
func ExampleOutcome_unmeasuredSeverity() {
	// severity renders the measurement or its absence — the absence is a
	// distinct state, never folded into the numeric range.
	severity := func(o outcome.Outcome) string {
		if o.ObservedSeverity == nil {
			return "unmeasured"
		}
		return strconv.FormatFloat(*o.ObservedSeverity, 'f', 2, 64)
	}

	measured := 0.60
	fmt.Println(severity(outcome.Outcome{Result: outcome.ResultSuccess}))
	fmt.Println(severity(outcome.Outcome{Result: outcome.ResultSuccess, ObservedSeverity: &measured}))

	// Output:
	// unmeasured
	// 0.60
}

// ExampleResult shows the vocabulary wide enough to say "it half-worked and
// isn't settling" — a result set that can only say success or failure has to
// round that case to one of the two lies.
func ExampleResult() {
	for _, r := range []outcome.Result{
		outcome.ResultApplied,
		outcome.ResultSuccess,
		outcome.ResultPartialNonConverging,
	} {
		fmt.Println(r)
	}

	// Output:
	// applied
	// success
	// partial_non_converging
}
