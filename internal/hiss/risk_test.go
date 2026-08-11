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
		automatic  bool
		blast      proposal.BlastTier
		want       decision.Band
	}{
		"riskBand auto-clears a reversible low-blast action":    {true, true, proposal.BlastLow, decision.BandActReversible},
		"riskBand auto-clears a reversible medium-blast action": {true, true, proposal.BlastMed, decision.BandActReversible},
		"riskBand disrupts a reversible high-blast action":      {true, true, proposal.BlastHigh, decision.BandActDisruptive},
		"riskBand disrupts an irreversible low-blast action":    {false, true, proposal.BlastLow, decision.BandActDisruptive},
		"riskBand disrupts an irreversible medium-blast action": {false, true, proposal.BlastMed, decision.BandActDisruptive},
		"riskBand disrupts an irreversible high-blast action":   {false, true, proposal.BlastHigh, decision.BandActDisruptive},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := hiss.RiskBand(tc.reversible, tc.automatic, tc.blast)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("riskBand cell wrong (-want +got):", diff)
			}
		})
	}
}

// TestRiskBand_KeepsAnUndoThatNeedsAHumanOutOfTheAutoEligibleBand pins the
// third state. A GitOps revert is authored as a reversal and is genuinely
// one, but the engine can only cut it — a reviewer lands it. Banding that
// as act_reversible would let the auto-eligible tier fire an action whose
// undo nobody has agreed to finish, which is the stamped-not-executed
// reversal I-12 records as closed.
func TestRiskBand_KeepsAnUndoThatNeedsAHumanOutOfTheAutoEligibleBand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		reversible bool
		automatic  bool
		blast      proposal.BlastTier
		want       decision.Band
	}{
		"RiskBand returns act_reversible for a low-blast undo the engine completes itself": {
			reversible: true, automatic: true, blast: proposal.BlastLow,
			want: decision.BandActReversible,
		},
		"RiskBand returns act_disruptive for a low-blast undo that needs a human to land": {
			reversible: true, automatic: false, blast: proposal.BlastLow,
			want: decision.BandActDisruptive,
		},
		"RiskBand returns act_disruptive for a wide undo the engine completes itself": {
			reversible: true, automatic: true, blast: proposal.BlastHigh,
			want: decision.BandActDisruptive,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := hiss.RiskBand(tc.reversible, tc.automatic, tc.blast)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong risk band for the authored reversal", diff)
			}
		})
	}
}
