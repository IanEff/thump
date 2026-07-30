package clank_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

// TestScoreConfidences_LikelihoodTermIsLiveWhenChangeEventsExist is the
// regression that would have caught noopChange{} going unconditional: a
// Set carrying CausalScores must come out with a different Confidence than
// the same Set with none, because the causal term is meaningless (and must
// drop out of the product entirely) whenever the SAO had no change events
// to score. Before ArgoChangeSource, CausalScores was always empty in
// production, so the "with events" branch below never ran on a rig.
func TestScoreConfidences_LikelihoodTermIsLiveWhenChangeEventsExist(t *testing.T) {
	t.Parallel()

	sao := proposal.SAO{Signal: proposal.SignalSnapshot{Confidence: 0.9}}
	cand := proposal.Candidate{ID: "p1", Confidence: 0.95, Citations: []string{"metrics_q", "loki_q"}}
	evidence := []proposal.EvidenceRef{
		{Query: "metrics_q", Live: true},
		{Query: "loki_q", Live: true},
	}

	tests := map[string]struct {
		causalScores []proposal.CausalScore
		want         float64
	}{
		"scoreConfidences multiplies in a real Likelihood when the SAO carried a change event": {
			causalScores: []proposal.CausalScore{{EventID: "c1", Likelihood: 0.6}},
			want:         0.9 * 1.0 * 0.6, // SignalConfidence * GroundingMany * Likelihood
		},
		"scoreConfidences leaves the causal term out entirely when the SAO carried no change events": {
			causalScores: nil,
			want:         0.9 * 1.0, // no Likelihood factor — LikelihoodOK is false
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

			if diff := cmp.Diff(tc.want, set.Proposals[0].Confidence); diff != "" {
				t.Error("wrong confidence after scoreConfidences (-want +got)\n", diff)
			}
		})
	}
}
