package hiss_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/hiss"
)

func TestRiskBand_EveryCell(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		reversible bool
		blast      proposal.BlastTier
		want       decision.Band
	}{
		"riskBand auto-clears a reversible low-blast action":    {true, proposal.BlastLow, decision.BandActReversible},
		"riskBand auto-clears a reversible medium-blast action": {true, proposal.BlastMed, decision.BandActReversible},
		"riskBand disrupts a reversible high-blast action":      {true, proposal.BlastHigh, decision.BandActDisruptive},
		"riskBand disrupts an irreversible low-blast action":    {false, proposal.BlastLow, decision.BandActDisruptive},
		"riskBand disrupts an irreversible medium-blast action": {false, proposal.BlastMed, decision.BandActDisruptive},
		"riskBand disrupts an irreversible high-blast action":   {false, proposal.BlastHigh, decision.BandActDisruptive},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := hiss.RiskBand(tc.reversible, tc.blast)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("riskBand cell wrong (-want +got):", diff)
			}
		})
	}
}
