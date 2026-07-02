package thump

import (
	"time"

	"github.com/ianeff/clank/internal/contract"
	"github.com/ianeff/clank/internal/decision"
)

type Order struct {
	ID          string                   `json:"id,omitempty" yaml:"id,omitempty"` // "ord:" + SignalRef + ":" + unix(now)
	DecisionRef string                   `json:"decisionRef,omitempty" yaml:"decisionRef,omitempty"`
	SignalRef   string                   `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`
	ContractRef string                   `json:"contractRef,omitempty" yaml:"contractRef,omitempty"`
	GrantedBand decision.Band            `json:"grantedBand,omitempty" yaml:"grantedBand,omitempty"` // rides along so a live executor can enforce band ≤ grant (PARKED)
	Description string                   `json:"description,omitempty" yaml:"description,omitempty"` // contract.Action.Description, verbatim
	Parameters  map[string]float64       `json:"parameters,omitempty" yaml:"parameters,omitempty"`   // ScopeParameters → Default. thump INVENTS NO NUMBERS (I-4, Claim 9)
	Reversal    ReversalPlan             `json:"reversal,omitempty" yaml:"reversal,omitempty"`
	Success     contract.SuccessCriteria `json:"success,omitempty" yaml:"success,omitempty"` // rendered, not evaluated, in v1 (PARKED: the convergence watcher)
	RenderedAt  time.Time                `json:"renderedAt,omitempty" yaml:"renderedAt,omitempty"`
}

type ReversalPlan struct {
	Method   string `json:"method,omitempty" yaml:"method,omitempty"`     // candidate.ReversalPath.Method
	Watching string `json:"watching,omitempty" yaml:"watching,omitempty"` // candidate.ReversalPath.Watching
	Trigger  string `json:"trigger,omitempty" yaml:"trigger,omitempty"`   // candidate.ReversalPath.Trigger
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"` // contract.Reversal.Fallback
}
