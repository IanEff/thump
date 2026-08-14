package grade_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/grade"
)

func TestFromRecord_SettlesARunFromWhatHappenedNext(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		set    proposal.Set
		dec    decision.Decision
		out    outcome.Outcome
		want   grade.Label
		wantOK bool
	}{
		"FromRecord settles an executed action as correct when its incident converged inside the window": {
			set:    proposal.Set{RunID: "fp/1"},
			out:    outcome.Outcome{Result: outcome.ResultSuccess},
			want:   grade.Label{RunID: "fp/1", Correct: true, Source: grade.SourceConverged},
			wantOK: true,
		},
		"FromRecord settles an executed action as incorrect when thump had to undo it": {
			set:    proposal.Set{RunID: "fp/2"},
			out:    outcome.Outcome{Result: outcome.ResultPartialNonConverging, Error: "never settled"},
			want:   grade.Label{RunID: "fp/2", Correct: false, Source: grade.SourceReversed},
			wantOK: true,
		},
		"FromRecord settles an escalated candidate as correct when an operator approved it anyway": {
			set:    proposal.Set{RunID: "fp/3"},
			dec:    decision.Decision{Verdict: decision.VerdictEscalate, Approver: "dummy operator"},
			want:   grade.Label{RunID: "fp/3", Correct: true, Source: grade.SourceApproved},
			wantOK: true,
		},
		"FromRecord refuses to settle a run nobody ever ruled on": {
			set:    proposal.Set{RunID: "fp/4"},
			wantOK: false,
		},
		"FromRecord refuses to settle an action still inside its convergence window": {
			set:    proposal.Set{RunID: "fp/5"},
			out:    outcome.Outcome{Result: outcome.ResultApplied},
			wantOK: false, // ResultApplied is interim; proposal.go:106 and CollapseCases both say so
		},
		"FromRecord refuses to settle a dry run, which touched nothing": {
			set:    proposal.Set{RunID: "fp/6"},
			out:    outcome.Outcome{Mode: outcome.ModeDryRun, Result: outcome.ResultRendered},
			wantOK: false,
		},
		"FromRecord refuses to settle a held candidate an operator approved, since Hold speaks to risk not confidence": {
			set:    proposal.Set{RunID: "fp/7"},
			dec:    decision.Decision{Verdict: decision.VerdictHold, Approver: "dummy operator"},
			wantOK: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := grade.FromRecord(tc.set, tc.dec, tc.out)
			if ok != tc.wantOK {
				t.Fatalf("wrong settled-ness for %q: want %v, got %v", name, tc.wantOK, ok)
			}
			if !tc.wantOK {
				return
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong label derived from the record", diff)
			}
		})
	}
}
