// Package incident is the operator's read-model of the THUMP stream: it
// folds the boundary objects rattle, clank, hiss, and thump emit into one
// Incident per fingerprint. It carries no model and no tools, so it can
// only replay what the beats already emitted — that incapacity is what
// keeps it from becoming a second inference engine.
package incident

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
	StageDetected Stage = "detected" // rattle emitted a Detection; nothing downstream yet
	StageProposed Stage = "proposed" // clank emitted a proposal.Set
	StageDecided  Stage = "decided"  // hiss ruled; the verdict on Governed says how
	StageApplied  Stage = "applied"  // thump executed; convergence not yet known
	StageSettled  Stage = "settled"  // terminal: success, failure, or partial_non_converging
)

// Incident is one fingerprint's journey collapsed to its current Stage —
// derived and rebuildable from the stream, never a source of truth on its
// own.
type Incident struct {
	Fingerprint string    `json:"fingerprint,omitempty"`
	Stage       Stage     `json:"stage,omitempty"`
	Service     string    `json:"service,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Governed is the latest decision hiss or a break-glass force issued for
	// this fingerprint — whether a human still owes it an ack is
	// Verdict.AwaitsApproval, never a second field here that could disagree.
	Governed *decision.Governed `json:"governed,omitempty"`
	// Severity is the latest ObservedSeverity, pointer-preserved: nil means
	// unmeasured, never a fabricated 0 sitting next to a real 0.60.
	Severity *float64 `json:"severity,omitempty"`
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
		next.Stage = StageDecided
		next.Governed = &v
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
