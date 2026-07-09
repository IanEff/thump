package hiss

import (
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

type Authority struct{}

func (Authority) Evaluate(ps proposal.Set, pol Policy, now time.Time) decision.Decision {
	d := decision.Decision{
		ID:            fmt.Sprintf("dec:%s:%d", ps.SignalRef, now.Unix()),
		ProposalRef:   ps.Name,
		SignalRef:     ps.SignalRef,
		CandidateRef:  ps.Recommended,
		PolicyVersion: pol.Version,
		EvaluatedAt:   now,
	}

	rec, found := recommended(ps)
	if ps.Gate == nil || !ps.Gate.Passed || !found {
		d.Verdict = decision.VerdictRejected
		d.Reasons = []string{ReasonUngatedInput}
		return d
	}

	d.RequestedBand = requestedBand(rec)
	d.FloorApplied = pol.Floors[ps.ServiceTier][ps.FailureClass]

	if rec.Confidence < d.FloorApplied {
		d.Reasons = append(d.Reasons, ReasonConfidenceFloor)
	}
	if bandRank(d.RequestedBand) > bandRank(pol.MaxBand[ps.ServiceTier]) {
		d.Reasons = append(d.Reasons, ReasonAuthorityCeiling)
	}
	if pol.RequireReversal && rec.ReversalPath == nil {
		d.Reasons = append(d.Reasons, ReasonIrreversible)
	}

	for _, w := range pol.FreezeWindows {
		if !now.Before(w.Start) && now.Before(w.End) {
			d.Reasons = append(d.Reasons, ReasonFreezeWindow+":"+w.Name)
		}
	}

	if len(d.Reasons) > 0 {
		d.Verdict = decision.VerdictEscalate
		return d
	}
	d.Verdict = decision.VerdictApproved
	d.GrantedBand = d.RequestedBand
	return d
}

func bandRank(b decision.Band) int {
	switch b {
	case decision.BandObserve:
		return 0
	case decision.BandActDisruptive:
		return 2
	case decision.BandActReversible:
		return 1
	default:
		return 3
	}
}

const (
	ReasonConfidenceFloor  = decision.ReasonConfidenceFloor
	ReasonAuthorityCeiling = decision.ReasonAuthorityCeiling
	ReasonIrreversible     = decision.ReasonIrreversible
	ReasonFreezeWindow     = decision.ReasonFreezeWindow
	ReasonUngatedInput     = decision.ReasonUngatedInput
)

func recommended(ps proposal.Set) (proposal.Candidate, bool) {
	for _, c := range ps.Proposals {
		if c.ID == ps.Recommended {
			return c, true
		}
	}
	return proposal.Candidate{}, false
}

func requestedBand(c proposal.Candidate) decision.Band {
	if c.GovernanceLevel == nil {
		return decision.BandObserve // absence != privilege
	}
	return decision.Band(c.GovernanceLevel.Band)
}
