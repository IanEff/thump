package clank_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

// TestScoreConfidences_OnlyInTopologyCausalScoresMoveConfidence pins the
// discrimination the causal term exists to provide. A change somewhere else
// in the cluster is not a weak cause; it is not a cause. If it were allowed
// to multiply in, holding uncorrelatable change data would be strictly worse
// than holding none — and since every score for an out-of-topology target
// caps at the same defence-1 ceiling, the term would read the same number on
// every run and carry no information at all.
func TestScoreConfidences_OnlyInTopologyCausalScoresMoveConfidence(t *testing.T) {
	t.Parallel()

	sao := proposal.SAO{Signal: proposal.SignalSnapshot{Confidence: 0.9}}
	cand := proposal.Candidate{ID: "p1", Confidence: 0.95, Citations: []string{"metrics_q", "loki_q"}}
	// Two backends, not two queries: the grounding tier counts distinct
	// EvidenceRef.Tool values, so a pair of refs that named no tool at all
	// would collapse to one source and pull every want below off the
	// GroundingMany tier this table is holding fixed.
	evidence := []proposal.EvidenceRef{
		{Tool: "metrics", Query: "metrics_q", Live: true},
		{Tool: "loki", Query: "loki_q", Live: true},
	}

	tests := map[string]struct {
		causalScores []proposal.CausalScore
		want         float64
	}{
		"scoreConfidences multiplies in a real Likelihood when a change event resolved into the topology": {
			causalScores: []proposal.CausalScore{{EventID: "c1", InTopology: true, Likelihood: 0.6}},
			want:         0.9 * 1.0 * 0.6, // SignalConfidence * GroundingMany * Likelihood
		},
		"scoreConfidences leaves the causal term out entirely when the SAO carried no change events": {
			causalScores: nil,
			want:         0.9 * 1.0, // no Likelihood factor — LikelihoodOK is false
		},
		"scoreConfidences leaves confidence untouched when every change event landed outside the topology": {
			causalScores: []proposal.CausalScore{{EventID: "c1", Likelihood: 0.5}, {EventID: "c2", Likelihood: 0.5}},
			want:         0.9 * 1.0, // identical to carrying no change data at all
		},
		"scoreConfidences ignores a higher out-of-topology Likelihood in favour of the in-topology one": {
			causalScores: []proposal.CausalScore{
				{EventID: "c1", InTopology: true, Likelihood: 0.4},
				{EventID: "c2", Likelihood: 0.9},
			},
			want: 0.9 * 1.0 * 0.4, // the max is taken over in-topology scores only, not filtered-then-maxed-over-all
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			set := proposal.Set{
				Proposals:    []proposal.Candidate{cand},
				Evidence:     evidence,
				SAOSnapshot:  &sao,
				CausalScores: tc.causalScores,
			}

			clank.ScoreConfidencesForTest(&set, sao, nil, "fp", clank.DefaultScoringWeights())

			if diff := cmp.Diff(tc.want, set.Proposals[0].Confidence, cmpopts.EquateApprox(1e-9, 1e-9)); diff != "" {
				t.Error("wrong confidence after scoreConfidences (-want +got)\n", diff)
			}
		})
	}
}
