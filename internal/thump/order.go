package thump

import (
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
)

// Order is what Actuator.Render produced from one governed approval — the
// rendered, not-yet-executed description of the action thump is about to
// (dry-run) perform. Every field traces back to the Decision, the Set's
// recommended Candidate, or the matched ActionContract; Render invents no
// value that isn't already sitting in one of those three.
type Order struct {
	ID          string                   `json:"id,omitempty" yaml:"id,omitempty"`     // "ord:" + SignalRef + ":" + unix(now)
	Kind        OrderKind                `json:"kind,omitempty" yaml:"kind,omitempty"` // forward (zero value) or reversal — the one bit a kill-switch reads to exempt cleanup; Render leaves it unset, only ReversalWatcher stamps a reversal
	DecisionRef string                   `json:"decisionRef,omitempty" yaml:"decisionRef,omitempty"`
	SignalRef   string                   `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`
	ContractRef string                   `json:"contractRef,omitempty" yaml:"contractRef,omitempty"`
	GrantedBand decision.Band            `json:"grantedBand,omitempty" yaml:"grantedBand,omitempty"` // carried for a future live executor to enforce band <= grant; read by nothing in v1
	Description string                   `json:"description,omitempty" yaml:"description,omitempty"` // contract.Action.Description, verbatim
	Parameters  map[string]float64       `json:"parameters,omitempty" yaml:"parameters,omitempty"`   // ScopeParameters -> Default, verbatim from the catalog; thump invents no numbers of its own
	Reversal    ReversalPlan             `json:"reversal" yaml:"reversal,omitempty"`
	Success     contract.SuccessCriteria `json:"success" yaml:"success,omitempty"` // rendered, not evaluated, in v1 — no convergence watcher exists yet to check it
	RenderedAt  time.Time                `json:"renderedAt" yaml:"renderedAt,omitempty"`
	// Notes is the human-readable case for this action, rendered from the
	// whole ranked Set — the alternatives, their confidence, their citations,
	// and why the winner won. Empty for a mutation; a maintenance release
	// carries it into the artifact a reviewer reads.
	Notes string `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// OrderKind separates a forward action from its undo — the distinction a
// kill-switch needs to refuse all new blast radius while never stranding
// in-flight cleanup half-done. The zero value is a forward order, so every
// Order Render already produces stays gated as forward untouched.
type OrderKind string

const (
	OrderForward  OrderKind = ""         // a new action — subject to the kill-switch
	OrderReversal OrderKind = "reversal" // an undo of an already-executed action — exempt from the kill-switch
)

// ReversalPlan is how to undo an Order. Method, Watching, and Trigger carry
// over from the granted Candidate's ReversalPath unchanged — embedded rather
// than restated, since contract.Reversal.Method is a separate, unused field
// on the catalog side and would collide with an embed of the whole type.
// Fallback and RestoreOnSuccess are the ActionContract's authored facts,
// never the model's, so a Candidate with no ReversalPath still carries them.
type ReversalPlan struct {
	proposal.ReversalPath
	Fallback         string `json:"fallback,omitempty" yaml:"fallback,omitempty"`                 // contract.Reversal.Fallback — the authored fallback if the reversal method itself fails
	RestoreOnSuccess bool   `json:"restoreOnSuccess,omitempty" yaml:"restoreOnSuccess,omitempty"` // contract.Reversal.RestoreOnSuccess — the catalog's declaration, never derived from the Candidate
	HoldOnMiss       bool   `json:"holdOnMiss,omitempty" yaml:"holdOnMiss,omitempty"`             // contract.Reversal.HoldOnMiss — the catalog's declaration, never derived from the Candidate
}
