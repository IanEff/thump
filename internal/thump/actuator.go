package thump

import (
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/decision"
	"github.com/ianeff/thump/internal/proposal"
)

var (
	ErrUnauditable    = errors.New("thump: decision fails its audit invariant")
	ErrUngoverned     = errors.New("thump: decision is not an approval")
	ErrSeamMismatch   = errors.New("thump: decision and set do not describe each other")
	ErrOutsideCatalog = errors.New("thump: granted contract not in catalog")
)

type Actuator struct{}

func (Actuator) Render(g decision.Governed, cat *contract.StaticCatalog, now time.Time) (Order, error) {
	dec, ps := g.Decision, g.Set

	if err := dec.Auditable(); err != nil {
		return Order{}, fmt.Errorf("%w: %w", ErrUnauditable, err)
	}
	if dec.Verdict != decision.VerdictApproved {
		return Order{}, fmt.Errorf("%w: verdict %q", ErrUngoverned, dec.Verdict)
	}
	cand, found := candidate(ps, dec.CandidateRef)
	if dec.ProposalRef != ps.Name || dec.SignalRef != ps.SignalRef || !found {
		return Order{}, fmt.Errorf("%w: decision %s vs set %s", ErrSeamMismatch, dec.ID, ps.Name)
	}
	ct, ok := cat.ByName(cand.ContractRef)
	if !ok {
		return Order{}, fmt.Errorf("%w: %q", ErrOutsideCatalog, cand.ContractRef)
	}

	o := Order{
		ID:          fmt.Sprintf("ord:%s:%d", dec.SignalRef, now.Unix()),
		DecisionRef: dec.ID,
		SignalRef:   dec.SignalRef,
		ContractRef: cand.ContractRef,
		GrantedBand: dec.GrantedBand,
		Description: ct.Action.Description,
		Success:     ct.SuccessCriteria,
		RenderedAt:  now,
	}
	if len(ct.Action.ScopeParameters) > 0 {
		o.Parameters = make(map[string]float64, len(ct.Action.ScopeParameters))
		for name, r := range ct.Action.ScopeParameters {
			o.Parameters[name] = r.Default
		}
	}
	if cand.ReversalPath != nil {
		o.Reversal = ReversalPlan{
			Method:   cand.ReversalPath.Method,
			Watching: cand.ReversalPath.Watching,
			Trigger:  cand.ReversalPath.Trigger,
			Fallback: ct.Reversal.Fallback,
		}
	}
	return o, nil
}

func candidate(ps proposal.Set, ref string) (proposal.Candidate, bool) {
	for _, c := range ps.Proposals {
		if c.ID == ref {
			return c, true
		}
	}
	return proposal.Candidate{}, false
}
