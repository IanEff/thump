// Package outcome is the boundary vocabulary of the Act beat (thump): the
// Outcome it emits after rendering (dry-run) or executing (live) the
// Decision it was granted. Every Outcome is born-auditable — Auditable() is
// the invariant every emitted Outcome must satisfy: no decision ref, no
// execution time, no mode, no result, or a failure/partial result with no
// error text are all refused, not silently allowed.
//
// v1 is additive-only: never rename, retype, or repurpose a field here, since
// other processes (not just other packages) depend on this exact shape.
package outcome

import (
	"errors"
	"fmt"
	"time"
)

// Outcome is thump's record of what actually happened rendering (dry-run) or
// executing (live) one Decision — never a prediction. PredictedImpact
// (api/v1/proposal) is the forecast; Outcome is the measurement.
type Outcome struct {
	ID               string    `json:"id,omitempty" yaml:"id,omitempty"`                   // deterministic: "out:" + SignalRef + ":" + unix(now)
	DecisionRef      string    `json:"decisionRef,omitempty" yaml:"decisionRef,omitempty"` // Decision.ID — the grant this outcome answers to
	SignalRef        string    `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`     // the fingerprint, threaded through untouched (4th beat, same thread)
	ContractRef      string    `json:"contractRef,omitempty" yaml:"contractRef,omitempty"` // what was (would have been) executed
	Mode             Mode      `json:"mode,omitempty" yaml:"mode,omitempty"`               // dry_run or live — rehearsal and reality must be distinguishable, never inferred from Result alone
	Result           Result    `json:"result,omitempty" yaml:"result,omitempty"`
	Error            string    `json:"error,omitempty" yaml:"error,omitempty"` // required company for failure / partial_non_converging — a failure with no error text is silence, not accountability
	ExecutedAt       time.Time `json:"executedAt,omitempty" yaml:"executedAt,omitempty"`
	ObservedSeverity *float64  `json:"observedSeverity,omitempty" yaml:"observedSeverity,omitempty"`
}

// Auditable is the invariant every emitted Outcome must satisfy: no
// DecisionRef, no ExecutedAt, no Mode, no Result, or a failure/
// partial_non_converging Result with no Error text are all refused outright,
// not silently allowed through.
func (o Outcome) Auditable() error {
	switch {
	case o.DecisionRef == "":
		return errors.New("outcome missing decision ref - an act answers to a grant")
	case o.ExecutedAt.IsZero():
		return errors.New("outcome missing execution time")
	case o.Mode == "":
		return errors.New("outcome missing mode - rehearsal and reality must be distinguishable")
	case o.Result == "":
		return errors.New("outcome missing result")
	case (o.Result == ResultFailure || o.Result == ResultPartialNonConverging) && o.Error == "":
		return fmt.Errorf("%s outcome with no error text is silence, not accountability", o.Result)
	}
	return nil
}

// Mode says whether the Decision was rendered without touching anything or
// actually executed — the distinction Outcome.Auditable refuses to leave
// unstated.
type Mode string

const (
	ModeDryRun Mode = "dry_run"
	ModeLive   Mode = "live"
)

// Result is the terminal state an Outcome reached — a closed enum wide
// enough to say "half-worked and isn't settling" instead of rounding every
// outcome to success or failure.
type Result string

const (
	ResultRendered Result = "rendered" // dry-run's only terminal state: the order exists, nothing was touched
	// ResultApplied is a live action's immediate word: the mutation ran with
	// no error, but whether the incident RESOLVED is not yet known — that's
	// the convergence watcher's word to have, after the success window. Maps
	// to PhaseActed (in-flight, still deduping); calibration skips it, since
	// there is nothing settled yet to score.
	ResultApplied Result = "applied"
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
	ResultUnknown Result = "unknown"
	// ResultBlocked is a live order a disarmed kill-switch refused — a
	// recorded refusal, never a silent skip, and never a failure, so it needs
	// no error text to be Auditable. Distinct from a decline (hiss's, before
	// any order) and from failure (the action ran and broke).
	ResultBlocked Result = "blocked"
	// ResultPartialNonConverging is representable FROM BIRTH — charter I-6
	// defence 4: binary success/failure is the belief-formation trap, and a
	// vocabulary that can't say "it half-worked and isn't settling" will
	// round it to one of the lies. v1 never emits it; click will.
	ResultPartialNonConverging Result = "partial_non_converging"
)
