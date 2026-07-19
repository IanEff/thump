// Package trim is the operator's read-model of the THUMP stream: it folds
// the boundary objects rattle, clank, hiss, and thump emit into one
// Incident per fingerprint. It carries no model and no tools, so it can
// only replay what the beats already emitted — that incapacity is what
// keeps it from becoming a second inference engine.
package trim

import (
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// Stage is where the engine stands on one fingerprint — set only from the
// latest boundary object bearing it, never recomputed from scratch.
type Stage string

const (
	StageDetected Stage = "detected"     // rattle emitted a Detection; nothing downstream yet
	StageProposed Stage = "proposed"     // clank emitted a proposal.Set
	StageHeld     Stage = "held-for-you" // hiss held it — waiting on a human ack
	StageApproved Stage = "approved"     // hiss (or a forced human) granted it; thump not done yet
	StageDeclined Stage = "declined"     // hiss escalated or rejected
	StageApplied  Stage = "applied"      // thump executed; convergence not yet known
	StageSettled  Stage = "settled"      // terminal: success, failure, or partial_non_converging
)

// Incident is one fingerprint's journey collapsed to its current Stage —
// derived and rebuildable from the stream, never a source of truth on its
// own.
type Incident struct {
	Fingerprint string    `json:"fingerprint,omitempty"`
	Stage       Stage     `json:"stage,omitempty"`
	Service     string    `json:"service,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	// Held retains the exact Governed hiss judged while Stage is
	// StageHeld, so a later ack can act on the Set hiss actually saw. Nil
	// whenever Stage isn't StageHeld.
	Held *decision.Governed `json:"held,omitempty"`
	// Severity is the latest ObservedSeverity, pointer-preserved: nil
	// means unmeasured, never a fabricated 0 sitting next to a real 0.60.
	Severity *float64 `json:"severity,omitempty"`
	// Forced is true when the latest Governed's Decision was pushed through
	// trim's break-glass path rather than granted by hiss — rendered loud,
	// never as an earned approval.
	Forced bool `json:"forced,omitempty"`
	// Operator names who forced it. Only meaningful when Forced is true.
	Operator string `json:"operator,omitempty"`
}

// Fold advances prior by one boundary object. Every case has to start from
// prior and only change what obj actually tells it something new about —
// most objects don't carry Service or Fingerprint, so those fields have to
// survive by inheritance or they'd zero out the moment an object that
// doesn't mention them arrives.
func Fold(prior Incident, obj any) Incident {
	next := prior

	switch v := obj.(type) {
	case signal.Detection:
		next.Fingerprint = v.Fingerprint
		next.Service = v.OriginService
		next.Stage = StageDetected
		next.UpdatedAt = v.DetectedAt
	case proposal.Set:
		next.Fingerprint = v.SignalRef
		next.Stage = StageProposed
		if v.SAOSnapshot != nil {
			next.UpdatedAt = v.SAOSnapshot.AssembledAt
		} else {
			next.UpdatedAt = prior.UpdatedAt
		}
	case decision.Governed:
		next.Fingerprint = v.Decision.SignalRef
		next.UpdatedAt = v.Decision.EvaluatedAt
		next.Forced = v.Decision.Forced
		next.Operator = v.Decision.Operator
		switch v.Decision.Verdict {
		case decision.VerdictHold:
			next.Stage = StageHeld
			next.Held = &v
		case decision.VerdictApproved:
			next.Stage = StageApproved
			next.Held = nil
		case decision.VerdictEscalate, decision.VerdictRejected:
			next.Stage = StageDeclined
			next.Held = nil
		}
	case outcome.Outcome:
		next.Fingerprint = v.SignalRef
		next.UpdatedAt = v.ExecutedAt
		next.Severity = v.ObservedSeverity
		switch v.Result {
		case outcome.ResultApplied:
			next.Stage = StageApplied
		default:
			next.Stage = StageSettled
		}

	default:
		// An object type Fold hasn't been taught yet — leave prior
		// exactly as it was. This is what makes the "ignores an unknown
		// object" subtest pass already, with zero code.
	}

	return next
}
