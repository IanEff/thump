package incident

import (
	"slices"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// Record is one fingerprint's whole journey, holding the boundary objects the
// beats emitted rather than fields copied off them — a projection that owned
// its own copy of Confidence or Verdict would be a second place those values
// could disagree with the stream, which is the one thing a read-model must
// never be. Incident stays embedded unchanged: the status line is what a list
// view needs, the objects are what a judgement needs.
type Record struct {
	Incident
	Detected *signal.Detection  `json:"detected,omitempty"`
	Proposed *proposal.Set      `json:"proposed,omitempty"`
	Decided  *decision.Decision `json:"decided,omitempty"`
	// Settled is every terminal Outcome in arrival order — the convergence
	// read first, then the undo's if one ran. A slice, not a field, because
	// "the undo was held" and "the undo ran and failed" are different facts
	// and a single Outcome cannot carry both.
	Settled []outcome.Outcome `json:"settled,omitempty"`
}

// FoldRecord advances prior by one boundary object, preserving the raw
// boundary objects alongside the folded Incident status line. Missing
// boundary objects remain nil — the read model never fabricates a zero-value
// boundary object for an unobserved stage.
func FoldRecord(prior Record, obj any) Record {
	next := prior
	next.Incident = Fold(prior.Incident, obj)

	switch v := obj.(type) {
	case signal.Detection:
		val := v
		next.Detected = &val
	case *signal.Detection:
		if v != nil {
			val := *v
			next.Detected = &val
		}
	case proposal.Set:
		val := v
		next.Proposed = &val
	case *proposal.Set:
		if v != nil {
			val := *v
			next.Proposed = &val
		}
	case decision.Governed:
		val := v.Decision
		next.Decided = &val
	case *decision.Governed:
		if v != nil {
			val := v.Decision
			next.Decided = &val
		}
	case decision.Decision:
		val := v
		next.Decided = &val
	case *decision.Decision:
		if v != nil {
			val := *v
			next.Decided = &val
		}
	case outcome.Outcome:
		next.Settled = append(slices.Clone(prior.Settled), v)
	case *outcome.Outcome:
		if v != nil {
			next.Settled = append(slices.Clone(prior.Settled), *v)
		}
	}

	return next
}
