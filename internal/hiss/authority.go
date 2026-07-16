package hiss

import (
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

// Authority is hiss's stateless governor — Evaluate is a pure function of
// its three arguments, so the same (Set, Policy, now) always earns the same
// verdict and there is nothing here that needs to persist between calls.
type Authority struct{}

// Evaluate governs one proposal.Set to exactly one decision.Decision. It
// never mutates or re-ranks the Set — clank's ranking stands; the only
// question Evaluate answers is whether the Recommended Candidate may
// proceed, and at what Band.
//
// A Set whose Gate didn't pass, or whose Recommended ID doesn't resolve to a
// Candidate in Proposals, is rejected outright with ReasonUngatedInput — an
// evidence gap upstream, not something hiss has standing to weigh in on.
// Otherwise the Candidate's requested Band is checked against four
// independent vetoes, any of which escalates rather than approves:
//
//   - ReasonConfidenceFloor — Confidence is below pol.Floors for the Set's
//     ServiceTier and FailureClass.
//   - ReasonAuthorityCeiling — the requested Band outranks pol.MaxBand for
//     the tier. A Candidate with no GovernanceLevel at all requests
//     BandObserve, the lowest rank — absence is never read as privilege. A
//     Band value bandRank doesn't recognize ranks above every real band, so
//     an unparseable one fails the ceiling too, rather than passing by
//     default.
//   - ReasonIrreversible — pol.RequireReversal is set and the Candidate
//     carries no ReversalPath. An action nobody can undo needs a human, not
//     a policy match.
//   - ReasonFreezeWindow — now falls inside one of pol.FreezeWindows.
//
// Zero reasons is required to reach stage two; any stage-one reason escalates
// rather than rejects — hiss is asking for a human, not overruling clank. A
// Candidate that clears stage one is eligible but not yet approved: stage two
// computes RiskBand from reversibility and blast tier alone (never from the
// requested Band or anything the model produced) and checks it against
// pol.AutoBand for the tier. Outranking it holds the Candidate for a human
// with ReasonRiskCeiling — eligible, but too much latitude to grant
// unattended — while staying inside it approves and grants the requested
// Band.
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

	// STAGE 2 — the shaper. Runs only once every stage-1 minimum is met; it
	// asks how much latitude an eligible Candidate gets, not eligibility.
	d.RiskBand = RiskBand(rec.ReversalPath != nil, rec.BlastTier)
	if bandRank(d.RiskBand) > bandRank(pol.AutoBand[ps.ServiceTier]) {
		d.Reasons = append(d.Reasons, ReasonRiskCeiling)
		d.Verdict = decision.VerdictHold
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

// Reason* re-export decision's reason vocabulary so callers reading hiss's
// verdicts don't need a second import to recognize them — see the
// decision.Reason* constants for what each one means.
const (
	ReasonConfidenceFloor  = decision.ReasonConfidenceFloor
	ReasonAuthorityCeiling = decision.ReasonAuthorityCeiling
	ReasonIrreversible     = decision.ReasonIrreversible
	ReasonFreezeWindow     = decision.ReasonFreezeWindow
	ReasonUngatedInput     = decision.ReasonUngatedInput
	ReasonRiskCeiling      = decision.ReasonRiskCeiling
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
