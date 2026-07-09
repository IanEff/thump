package thump

import (
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
)

var (
	// ErrUnauditable means the Decision fails its own Auditable() invariant —
	// missing a policy version, an evaluation time, or (on a non-approval) a
	// reason. thump refuses to act on a governance record it couldn't stand
	// behind later, whatever the verdict says.
	ErrUnauditable = errors.New("thump: decision fails its audit invariant")

	// ErrUngoverned means the Decision's Verdict isn't VerdictApproved.
	// escalate and rejected both refuse here — only hiss can turn either
	// into an approval; thump never acts on anything less.
	ErrUngoverned = errors.New("thump: decision is not an approval")

	// ErrSeamMismatch means the Decision and the proposal.Set it travels
	// with in one decision.Governed envelope don't describe each other: a
	// different ProposalRef or SignalRef, or a CandidateRef the Set doesn't
	// contain. hiss seals the envelope before handing it to thump, so a
	// mismatch here is a seam bug upstream, not a judgment call thump can
	// paper over.
	ErrSeamMismatch = errors.New("thump: decision and set do not describe each other")

	// ErrOutsideCatalog means the granted Candidate's ContractRef doesn't
	// resolve in cat — thump can render only what's catalogued, the same
	// autonomy boundary clank was bound by when it proposed the candidate.
	ErrOutsideCatalog = errors.New("thump: granted contract not in catalog")
)

// Actuator turns one governed approval into an Order — thump's only
// authoring step. Render reads the decision.Governed envelope and the
// catalog read-only and invents no values of its own: every Order field
// traces back to the Decision, the Set's recommended Candidate, or the
// matched ActionContract.
type Actuator struct{}

// Render converts a decision.Governed approval plus the ActionContract
// catalog into an Order, refusing rather than guessing at every seam: an
// unauditable Decision (ErrUnauditable), anything short of VerdictApproved
// (ErrUngoverned), a Decision/Set pair that doesn't describe each other
// (ErrSeamMismatch), or a granted contract absent from cat
// (ErrOutsideCatalog). Render never mutates g — the envelope is hiss's,
// read-only.
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
