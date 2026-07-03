package decision

import (
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/proposal"
)

type Decision struct {
	ID            string    `json:"id,omitempty" yaml:"id,omitempty"`
	ProposalRef   string    `json:"proposalRef,omitempty" yaml:"proposalRef,omitempty"` // ps.Name
	SignalRef     string    `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`
	CandidateRef  string    `json:"candidateRef,omitempty" yaml:"candidateRef,omitempty"`
	Verdict       Verdict   `json:"verdict,omitempty" yaml:"verdict,omitempty"`
	Reasons       []string  `json:"reasons,omitempty" yaml:"reasons,omitempty"`
	RequestedBand Band      `json:"requestedBand,omitempty" yaml:"requestedBand,omitempty"`
	GrantedBand   Band      `json:"grantedBand,omitempty" yaml:"grantedBand,omitempty"`
	FloorApplied  float64   `json:"floorApplied,omitempty" yaml:"floorApplied,omitempty"`
	PolicyVersion string    `json:"policyVersion,omitempty" yaml:"policyVersion,omitempty"`
	EvaluatedAt   time.Time `json:"evaluatedAt,omitempty" yaml:"evaluatedAt,omitempty"`
}

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

type Verdict string

const (
	VerdictApproved Verdict = "approved"
	VerdictEscalate Verdict = "escalate"
	VerdictRejected Verdict = "rejected"
)

type Band string

const (
	BandObserve       Band = "observe"        // rank 0 -the absent- request default
	BandActReversible Band = "act_reversible" // rank 1
	BandActDisruptive Band = "act_disruptive" // rank 2
)

const (
	ReasonConfidenceFloor  = "confidence_floor"
	ReasonAuthorityCeiling = "authority_ceiling"
	ReasonIrreversible     = "irreversible"
	ReasonFreezeWindow     = "freeze_window" // ":" + Window.Name appended
	ReasonUngatedInput     = "ungated_input" // also covers malformed sets — see Claim 8
)

// Governed is the hiss→thump seam envelope (Wave 0, option 2): the Decision
// travels with the Set it judged so thump never has to re-read a second
// file to find the candidate it was granted. hiss seals it; thump reads it
// read-only.
type Governed struct {
	Decision Decision     `json:"decision,omitempty" yaml:"decision,omitempty"`
	Set      proposal.Set `json:"set,omitempty" yaml:"set,omitempty"`
}
