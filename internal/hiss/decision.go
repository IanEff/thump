package hiss

import (
	"errors"
	"fmt"
	"time"
)

type Decision struct {
	ID            string
	ProposalRef   string // ps.Name
	SignalRef     string
	CandidateRef  string
	Verdict       Verdict
	Reasons       []string
	RequestedBand Band
	GrantedBand   Band
	FloorApplied  float64
	PolicyVersion string
	EvaluatedAt   time.Time
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

// func bandRank(b Band) int {
// 	switch b {
// 	case BandObserve:
// 		return 0
// 	case BandActDisruptive:
// 		return 2
// 	case BandActReversible:
// 		return 1
// 	}
// 	return 0
// }
