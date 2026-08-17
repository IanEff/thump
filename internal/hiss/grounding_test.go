package hiss_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

// TestAuthority_AWellGroundedReversibleCandidateIsNotVetoedByACautiousModel
// pins the split this phase exists for: Candidate.Confidence is
// min(computed, selfReported) (internal/clank/confidence.go:80), so today a
// model self-reporting 0.65 vetoes a candidate the engine grounded at 1.00 —
// even though RiskBand refuses to let model output shape risk at all
// (internal/hiss/risk.go:8-13) and thump's own success window would undo the
// action if it failed to converge.
func TestAuthority_AWellGroundedReversibleCandidateIsNotVetoedByACautiousModel(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		computed     float64
		selfReported float64
		reversal     *proposal.ReversalPath
		blast        proposal.BlastTier
		want         decision.Verdict
	}{
		"thin evidence is refused whatever the model claims, because grounding is the standard the floor was written for": {
			computed: 0.70, selfReported: 0.95,
			reversal: &proposal.ReversalPath{Automatic: true}, blast: proposal.BlastLow,
			want: decision.VerdictEscalate,
		},
		"grounded evidence on a self-undoing action approves despite the model hedging below the floor": {
			computed: 1.00, selfReported: 0.65,
			reversal: &proposal.ReversalPath{Automatic: true}, blast: proposal.BlastLow,
			want: decision.VerdictApproved,
		},
		"the same hedge still escalates when only a human can land the undo": {
			computed: 1.00, selfReported: 0.65,
			reversal: &proposal.ReversalPath{Automatic: false}, blast: proposal.BlastLow,
			want: decision.VerdictEscalate,
		},
		"the same hedge still escalates on a high-blast action even with an automatic undo": {
			computed: 1.00, selfReported: 0.65,
			reversal: &proposal.ReversalPath{Automatic: true}, blast: proposal.BlastHigh,
			want: decision.VerdictEscalate,
		},
		"an irreversible candidate is refused on reversal before confidence is ever consulted": {
			computed: 1.00, selfReported: 1.00,
			reversal: nil, blast: proposal.BlastLow,
			want: decision.VerdictEscalate,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ps := governedSet()
			ps.Proposals[0].ComputedConfidence = tc.computed
			ps.Proposals[0].Confidence = min(tc.computed, tc.selfReported)
			ps.Proposals[0].ReversalPath = tc.reversal
			ps.Proposals[0].BlastTier = tc.blast

			pol := calmPolicy()
			pol.ConfidenceGate = "split"

			got := decide(t, ps, pol)
			if diff := cmp.Diff(tc.want, got.Verdict); diff != "" {
				t.Errorf("verdict mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestAuthority_ASetPredatingComputedConfidenceIsGovernedOnItsOwnTermsNotAsUngrounded
// pins the replay hazard: ComputedConfidence is zero on any Set sealed before
// the field existed, and reading that as a grounding failure would retroactively
// escalate every historical approval the moment this gate ships.
func TestAuthority_ASetPredatingComputedConfidenceIsGovernedOnItsOwnTermsNotAsUngrounded(t *testing.T) {
	t.Parallel()

	ps := governedSet()
	ps.Proposals[0].ComputedConfidence = 0.0 // sealed before the field existed
	ps.Proposals[0].Confidence = 0.85        // above calmPolicy's 0.75 floor

	pol := calmPolicy()
	pol.ConfidenceGate = "split"

	got := decide(t, ps, pol)
	if diff := cmp.Diff(decision.VerdictApproved, got.Verdict); diff != "" {
		t.Errorf("pre-ComputedConfidence set must be approved on its recorded confidence (-want +got):\n%s", diff)
	}
}
