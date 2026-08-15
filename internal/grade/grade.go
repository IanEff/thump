// Package grade derives what the engine should have done from what
// happened next. A label is read out of the record — an outcome the
// convergence watcher settled, or a verdict an operator resolved — and
// never authored by hand, so the same rule holds on a cluster nobody
// here has seen.
package grade

import (
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

// Label is one settled judgement about a run. Source names the evidence
// that settled it, so a corpus row can be argued with rather than
// trusted.
type Label struct {
	RunID   string `json:"runID"`
	Correct bool   `json:"correct"`
	Source  Source `json:"source"`
}

// Source is the closed set of things that can settle a run — a
// convergence result or an operator's verdict, never an inference.
type Source string

const (
	SourceConverged Source = "converged" // the action ran and the incident settled inside its window
	SourceReversed  Source = "reversed"  // the action ran, missed its window, and thump undid it
	SourceApproved  Source = "approved"  // hiss withheld it and an operator approved it anyway
	SourceDenied    Source = "denied"    // hiss withheld it and an operator refused it
)

// FromRecord settles set from the decision and outcome that answered it.
// ok is false whenever nothing in the record settles the question — a
// declined run, an action still inside its convergence window, or a Hold
// an operator approved, which speaks to the risk lattice and not to
// whether clank's confidence was right — which is a population to count,
// never a row to guess at.
func FromRecord(set proposal.Set, dec decision.Decision, out outcome.Outcome) (Label, bool) {
	switch {
	case out.Result != "" && out.Result != outcome.ResultRendered && out.Result != outcome.ResultApplied:
		win := out.Result == outcome.ResultSuccess
		src := SourceReversed
		if win {
			src = SourceConverged
		}
		return Label{RunID: set.RunID, Correct: win, Source: src}, true
	case dec.Verdict == decision.VerdictEscalate && dec.Approver != "":
		return Label{RunID: set.RunID, Correct: true, Source: SourceApproved}, true
	default:
		return Label{}, false
	}
}
