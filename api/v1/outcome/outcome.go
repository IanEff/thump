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

type Outcome struct {
	ID          string    `json:"id,omitempty" yaml:"id,omitempty"`                   // deterministic: "out:" + SignalRef + ":" + unix(now)
	DecisionRef string    `json:"decisionRef,omitempty" yaml:"decisionRef,omitempty"` // Decision.ID — the grant this outcome answers to
	SignalRef   string    `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`     // the fingerprint, threaded through untouched (4th beat, same thread)
	ContractRef string    `json:"contractRef,omitempty" yaml:"contractRef,omitempty"` // what was (would have been) executed
	Mode        Mode      `json:"mode,omitempty" yaml:"mode,omitempty"`
	Result      Result    `json:"result,omitempty" yaml:"result,omitempty"`
	Error       string    `json:"error,omitempty" yaml:"error,omitempty"` // required company for failure / partial_non_converging
	ExecutedAt  time.Time `json:"executedAt,omitempty" yaml:"executedAt,omitempty"`
}

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

type Mode string

const (
	ModeDryRun Mode = "dry_run"
	ModeLive   Mode = "live"
)

type Result string

const (
	ResultRendered Result = "rendered" // dry-run's only terminal state: the order exists, nothing was touched
	ResultSuccess  Result = "success"
	ResultFailure  Result = "failure"
	ResultUnknown  Result = "unknown"
	// ResultPartialNonConverging is representable FROM BIRTH — charter I-6
	// defence 4: binary success/failure is the belief-formation trap, and a
	// vocabulary that can't say "it half-worked and isn't settling" will
	// round it to one of the lies. v1 never emits it; click will.
	ResultPartialNonConverging Result = "partial_non_converging"
)
