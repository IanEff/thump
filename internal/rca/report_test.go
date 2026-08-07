package rca_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/rca"
)

// TestReport_DerivesTheFloorFromTheTableRatherThanAnAuthoredConstant pins the
// bar to the population. The floor was a const whose comment did the
// subtraction in prose, and it went stale the first time a row stopped being a
// known miss.
func TestReport_DerivesTheFloorFromTheTableRatherThanAnAuthoredConstant(t *testing.T) {
	t.Parallel()

	want := 0
	for _, c := range rca.Table() {
		if !c.KnownMiss {
			want++
		}
	}
	if diff := cmp.Diff(want, rca.Floor()); diff != "" {
		t.Error("floor disagrees with the table it is derived from", diff)
	}
}

// TestReport_CountsOnlyPassingRowsTowardTheFloor keeps the tally honest: a
// known miss that happens to pass still counts, because the floor is a
// population bar and not a per-row exemption.
func TestReport_CountsOnlyPassingRowsTowardTheFloor(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		rows []rca.Row
		want int
	}{
		"NewReport scores zero when every row missed": {
			rows: []rca.Row{{Pass: false}, {Pass: false}},
			want: 0,
		},
		"NewReport counts a passing known miss toward the score": {
			rows: []rca.Row{{Pass: true, KnownMiss: true}, {Pass: false}},
			want: 1,
		},
		"NewReport counts every passing row": {
			rows: []rca.Row{{Pass: true}, {Pass: true}, {Pass: false}},
			want: 2,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, rca.NewReport(tc.rows).Scored); diff != "" {
				t.Error("wrong score", diff)
			}
		})
	}
}

// bracketedCase is the shape of the three confidence-graded rows in Table():
// a row that wants a real proposal, above a floor, with the model's
// self-report NOT binding.
func bracketedCase() rca.Case {
	return rca.Case{
		Name:                  "dummy graded row",
		WantDisposition:       "propose",
		WantClass:             proposal.FailureClass("redundancy_degraded"),
		WantConfidenceAtLeast: 0.65,
		WantCeilingBound:      false,
	}
}

// setAt builds a one-proposal Set at a given emitted confidence and
// ceiling-bound flag — the two fields ScoringWeights actually move.
func setAt(confidence float64, ceilingBound bool) proposal.Set {
	return proposal.Set{
		FailureClass: proposal.FailureClass("redundancy_degraded"),
		Proposals: []proposal.Candidate{{
			Confidence:             confidence,
			ComputedConfidence:     confidence,
			ConfidenceCeilingBound: ceilingBound,
		}},
	}
}

func TestGrade_ScoresARowAgainstTheBracketItsLabelsDescribe(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		set      proposal.Set
		wantPass bool
		wantMiss string
	}{
		"Grade fails a row whose confidence sits below the row's floor": {
			set:      setAt(0.60, false),
			wantPass: false,
			wantMiss: "confidence below the row's floor",
		},
		"Grade fails a row whose ceiling binds when the row wants it unbound": {
			set:      setAt(0.80, true),
			wantPass: false,
			wantMiss: "ceiling-bound disagreed with the row",
		},
		"Grade passes a row sitting inside the bracket both labels describe": {
			set:      setAt(0.80, false),
			wantPass: true,
			wantMiss: "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := rca.Grade(bracketedCase(), tc.set)

			if diff := cmp.Diff(tc.wantPass, got.Pass); diff != "" {
				t.Error("wrong pass verdict for the graded row", diff)
			}
			if diff := cmp.Diff(tc.wantMiss, got.Miss); diff != "" {
				t.Error("wrong miss reason for the graded row", diff)
			}
		})
	}
}
