// Package decision is the boundary vocabulary of the Governance Plane: the
// Decision that hiss emits after judging one proposal.Set, plus Governed, the
// envelope that carries a Decision and the Set it judged onward to thump.
// Every Decision is born-auditable — Auditable() is the invariant every
// Evaluate output must satisfy: a verdict without a policy version, an
// evaluation time, or (when not approved) a reason is not a valid Decision.
//
// v1 is additive-only: never rename, retype, or repurpose a field here, since
// other processes (not just other packages) depend on this exact shape.
package decision

import (
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
)

// Decision is hiss's one verdict on one proposal.Set — approved, escalate,
// or rejected. hiss never mutates or re-ranks the Set it judged; Decision
// only ever narrows or affirms the Recommended Candidate's requested band,
// it never substitutes a different one.
type Decision struct {
	ID            string    `json:"id,omitempty" yaml:"id,omitempty"`
	ProposalRef   string    `json:"proposalRef,omitempty" yaml:"proposalRef,omitempty"`   // the judged proposal.Set's Name
	SignalRef     string    `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`       // threaded through from the Set, unchanged — the same fingerprint since rattle
	CandidateRef  string    `json:"candidateRef,omitempty" yaml:"candidateRef,omitempty"` // the Set's Recommended Candidate ID this verdict was evaluated against
	Verdict       Verdict   `json:"verdict,omitempty" yaml:"verdict,omitempty"`
	Reasons       []string  `json:"reasons,omitempty" yaml:"reasons,omitempty"`             // one entry per veto that fired (Reason* constants below); empty only when Verdict is approved
	RequestedBand Band      `json:"requestedBand,omitempty" yaml:"requestedBand,omitempty"` // the Candidate's own GovernanceLevel.Band — BandObserve when the Candidate carried none, since absence is never read as privilege
	GrantedBand   Band      `json:"grantedBand,omitempty" yaml:"grantedBand,omitempty"`     // set only when Verdict is approved, and always equal to RequestedBand — hiss grants exactly what was asked or it doesn't grant at all
	RiskBand      Band      `json:"riskBand,omitempty" yaml:"riskBand,omitempty"`           // the intrinsic risk hiss's riskBand lattice computed from reversibility and blast tier, recorded alongside RequestedBand/GrantedBand so a hold verdict's cause is on the record
	FloorApplied  float64   `json:"floorApplied,omitempty" yaml:"floorApplied,omitempty"`   // the confidence floor looked up for the Set's ServiceTier and FailureClass from Policy.Floors
	PolicyVersion string    `json:"policyVersion,omitempty" yaml:"policyVersion,omitempty"` // which Policy this verdict was evaluated under — required by Auditable, since a verdict with no policy version can't be re-checked later
	EvaluatedAt   time.Time `json:"evaluatedAt,omitempty" yaml:"evaluatedAt,omitempty"`
	Forced        bool      `json:"forced,omitempty" yaml:"forced,omitempty"`     // true when a human pushed this through trim's break-glass path instead of hiss granting it — never rendered as an earned approval
	Operator      string    `json:"operator,omitempty" yaml:"operator,omitempty"` // who forced it; set only when Forced is true
}

// Auditable is the invariant every Authority.Evaluate output is tested
// against: a Decision missing its policy version or evaluation time, or one
// that isn't approved yet carries no Reasons, is refused outright — rejection
// is itself an audit record, never silence.
func (d Decision) Auditable() error {
	switch {
	case d.PolicyVersion == "":
		return errors.New("decision missing policy version")
	case d.EvaluatedAt.IsZero():
		return errors.New("decision missing evaluation time")
	case d.Verdict != VerdictApproved && len(d.Reasons) == 0:
		return fmt.Errorf("%s decision with no reasons is not accepted", d.Verdict)
	}
	return nil
}

// Verdict is hiss's judgment on a Set — rejection is an audit record, never
// silence, so a Decision always carries exactly one of these three.
type Verdict string

const (
	VerdictApproved Verdict = "approved" // zero Reasons; the Candidate may proceed at GrantedBand
	VerdictEscalate Verdict = "escalate" // one or more vetoes fired; hiss is asking for a human, not overruling clank
	VerdictRejected Verdict = "rejected" // the Set itself was ungated or malformed — not something hiss has standing to weigh in on
)

// Band ranks how much latitude a Candidate is asking for (or was granted) —
// observe < act_reversible < act_disruptive. Absence of a GovernanceLevel on
// a Candidate is read as BandObserve, the lowest rank, never as elevated
// privilege; an unparseable Band value ranks above every real band, so it
// fails an authority-ceiling check rather than passing by default.
type Band string

const (
	BandObserve       Band = "observe"        // rank 0 — the default when a Candidate carries no GovernanceLevel at all
	BandActReversible Band = "act_reversible" // rank 1 — the catalogued action carries a ReversalPath
	BandActDisruptive Band = "act_disruptive" // rank 2 — the catalogued action has no ReversalPath
)

// Reason* enumerate why a Decision escalated or rejected rather than
// approved — Authority.Evaluate can append more than one to a single
// Decision.Reasons; any one of them is enough to withhold approval.
const (
	ReasonConfidenceFloor  = "confidence_floor"  // the Recommended Candidate's Confidence is below Policy.Floors for this tier/class
	ReasonAuthorityCeiling = "authority_ceiling" // the requested Band outranks Policy.MaxBand for this tier
	ReasonIrreversible     = "irreversible"      // Policy.RequireReversal is set and the Candidate carries no ReversalPath
	ReasonFreezeWindow     = "freeze_window"     // ":" + Window.Name is appended — now falls inside a declared freeze window
	ReasonUngatedInput     = "ungated_input"     // the Set's Gate didn't pass, or Recommended didn't resolve to a Proposals entry — an evidence gap upstream, not hiss's call to make
)

// Governed is the hiss→thump seam envelope (Wave 0, option 2): the Decision
// travels with the Set it judged so thump never has to re-read a second
// file to find the candidate it was granted. hiss seals it; thump reads it
// read-only.
type Governed struct {
	Decision Decision     `json:"decision,omitempty" yaml:"decision,omitempty"` // the verdict — always about the Set carried alongside it, never a different one
	Set      proposal.Set `json:"set,omitempty" yaml:"set,omitempty"`           // the exact Set hiss judged, unmutated and un-re-ranked
}

const VerdictHold Verdict = "hold" // every minimum met, but risk exceeds the auto-fire ceiling — approved-in-principle, pending a human ack

const ReasonRiskCeiling = "risk_ceiling" // the computed risk band outranks Policy.AutoBand for this tier
