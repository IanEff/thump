package hiss

import (
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

// RiskBand is the shaper: an action's intrinsic risk, computed from authored
// facts only — never from anything the model produced. reversible is the
// ReversalPath bit; blast is authored on the ActionContract and copied onto
// the Candidate. Every cell of the 2×3 lattice is pinned by a test.
func RiskBand(reversible bool, blast proposal.BlastTier) decision.Band {
	switch {
	case !reversible:
		return decision.BandActDisruptive // no undo — always wants a human, whatever the blast
	case blast == proposal.BlastHigh:
		return decision.BandActDisruptive // reversible but wide — still wants a human
	default:
		return decision.BandActReversible // reversible + low/med — auto-eligible
	}
}
