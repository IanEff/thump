// Package approval is the operator->hiss ack surface: the Approval an
// operator emits through `trim approve` to release a held Candidate.
package approval

import (
	"errors"
	"time"
)

// Approval is one human ack of one held fingerprint.  Dedupe is on
// the SignalRef and nothing else.
type Approval struct {
	SignalRef  string    `json:"signalRef,omitempty"`  // the held Detection's fingerprint
	Approver   string    `json:"approver,omitempty"`   // Who approved.
	ApprovedAt time.Time `json:"approvedAt,omitempty"` // When the approval was made.
	// CapBand is the highest band the operator will grant.  Empty means
	// 'grant what hiss grants'.  Held as a string, not decision.Band to
	// keep this a leaf.
	CapBand string `json:"capBand,omitempty"`
}

// Auditable is the invariant every emitted Approval must satisfy.
func (a Approval) Auditable() error {
	switch {
	case a.SignalRef == "":
		return errors.New("approval missing signal ref - an ack answers to a held fingerprint")
	case a.Approver == "":
		return errors.New("approval missing approver - an ack with no human is not accountability")
	case a.ApprovedAt.IsZero():
		return errors.New("approval missing ack time")
	}
	return nil
}
