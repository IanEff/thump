package hiss

import (
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

// RiskBand is the shaper: an action's intrinsic risk, computed from authored
// facts only — never from anything the model produced. reversible is the
// ReversalPath bit; automatic is whether the engine can finish that undo
// itself, rather than a human landing it; blast is authored on the
// ActionContract and copied onto the Candidate. Every cell of the lattice is
// pinned by a test.
func RiskBand(reversible, automatic bool, blast proposal.BlastTier) decision.Band {
	switch {
	case !reversible:
		return decision.BandActDisruptive // no undo — always wants a human, whatever the blast
	case !automatic:
		return decision.BandActDisruptive // an undo exists, but only a human can land it
	case blast == proposal.BlastHigh:
		return decision.BandActDisruptive // reversible and self-completing, but still wide
	default:
		return decision.BandActReversible // reversible, self-completing, low/med blast
	}
}
