package thump

import (
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/contract"
)

// Order is what Actuator.Render produced from one governed approval — the
// rendered, not-yet-executed description of the action thump is about to
// (dry-run) perform. Every field traces back to the Decision, the Set's
// recommended Candidate, or the matched ActionContract; Render invents no
// value that isn't already sitting in one of those three.
type Order struct {
	ID          string                   `json:"id,omitempty" yaml:"id,omitempty"` // "ord:" + SignalRef + ":" + unix(now)
	DecisionRef string                   `json:"decisionRef,omitempty" yaml:"decisionRef,omitempty"`
	SignalRef   string                   `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`
	ContractRef string                   `json:"contractRef,omitempty" yaml:"contractRef,omitempty"`
	GrantedBand decision.Band            `json:"grantedBand,omitempty" yaml:"grantedBand,omitempty"` // carried for a future live executor to enforce band <= grant; read by nothing in v1
	Description string                   `json:"description,omitempty" yaml:"description,omitempty"` // contract.Action.Description, verbatim
	Parameters  map[string]float64       `json:"parameters,omitempty" yaml:"parameters,omitempty"`   // ScopeParameters -> Default, verbatim from the catalog; thump invents no numbers of its own
	Reversal    ReversalPlan             `json:"reversal,omitempty" yaml:"reversal,omitempty"`
	Success     contract.SuccessCriteria `json:"success,omitempty" yaml:"success,omitempty"` // rendered, not evaluated, in v1 — no convergence watcher exists yet to check it
	RenderedAt  time.Time                `json:"renderedAt,omitempty" yaml:"renderedAt,omitempty"`
}

// ReversalPlan is how to undo an Order, carried over from the granted
// Candidate's ReversalPath plus the ActionContract's authored Fallback. A
// Candidate with no ReversalPath renders a zero-value ReversalPlan — Render
// does not invent one.
type ReversalPlan struct {
	Method   string `json:"method,omitempty" yaml:"method,omitempty"`     // candidate.ReversalPath.Method — how to reverse the action
	Watching string `json:"watching,omitempty" yaml:"watching,omitempty"` // candidate.ReversalPath.Watching — the signal that says a reversal is needed
	Trigger  string `json:"trigger,omitempty" yaml:"trigger,omitempty"`   // candidate.ReversalPath.Trigger — the condition on that signal that fires the reversal
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"` // contract.Reversal.Fallback — the authored fallback if the reversal method itself fails
}
