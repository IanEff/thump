package clank_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

// TestPropose_AnUnstatedConfidenceIsNoCeilingRatherThanAZeroOne pins the
// difference between a ceiling the model declined to assert and one it set to
// zero. The self-report is applied as min(computed, stated): 1.0 is that
// operation's identity and 0 is its annihilator, so reading an absent number as
// the zero value drives every candidate in the set to zero no matter what the
// run grounded — and a candidate at zero confidence can never clear a policy
// floor, so no decision can ever reach a hold and no held action can ever reach
// a human.
func TestPropose_AnUnstatedConfidenceIsNoCeilingRatherThanAZeroOne(t *testing.T) {
	t.Parallel()

	// Each engine gets its own ledger, so the second run isn't deduplicated
	// against the first as a repeat of the same fingerprint.
	propose := func(t *testing.T, cand proposal.Candidate) float64 {
		t.Helper()
		model := &fakeModel{script: []clank.Completion{
			{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
			{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
				FailureClass: proposal.ClassDependencySaturation,
				Proposals:    []proposal.Candidate{cand},
			})}}},
		}}
		e, _ := newTestEngine(model)
		got, err := e.Propose(context.Background(), sigBurnAccel())
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Proposals) != 1 {
			t.Fatalf("want exactly one candidate to grade, got %d", len(got.Proposals))
		}
		return got.Proposals[0].Confidence
	}

	base := proposal.Candidate{ID: "p1", ContractRef: "throttle-non-critical-paths", Citations: []string{`{"q":"latency_p99"}`}}

	// Confidence is omitempty on the boundary object, so a zero value here
	// marshals to propose args with no confidence key at all — the exact shape
	// a live Haiku call produced.
	unstated := propose(t, base)
	if unstated == 0 {
		t.Fatal("a candidate whose model stated no confidence emitted zero, so nothing this run grounded can ever reach a policy floor")
	}

	statedCeilingOfOne := base
	statedCeilingOfOne.Confidence = 1
	if diff := cmp.Diff(statedCeilingOfOne.Confidence, 1.0); diff != "" {
		t.Fatal("fixture no longer states the identity ceiling", diff)
	}
	if diff := cmp.Diff(propose(t, statedCeilingOfOne), unstated, cmpopts.EquateApprox(1e-9, 1e-9)); diff != "" {
		t.Error("an unstated confidence must emit what an explicit ceiling of 1.0 emits (-want +got)\n", diff)
	}

	statedZero := base
	statedZero.Confidence = math.SmallestNonzeroFloat64 // a stated zero is unrepresentable under omitempty; the smallest real ceiling stands in
	if got := propose(t, statedZero); got > 1e-300 {
		t.Error("an explicitly stated near-zero ceiling must still clamp the emitted confidence, got", got)
	}
}

// TestScoreConfidences_CorroboratingChangeRaisesConfidenceAboveHoldingNone pins
// the direction of the causal term, which the arithmetic table above cannot:
// every want there is a single number, and a set of numbers that are all wrong
// in the same direction still agrees with itself. As a multiplier the term was
// bounded above by 1 while LikelihoodOK dropped it out entirely when nothing
// resolved, so learning that a change caused the incident lowered the engine's
// confidence — and wiring any new change source was a regression by
// construction.
func TestScoreConfidences_CorroboratingChangeRaisesConfidenceAboveHoldingNone(t *testing.T) {
	t.Parallel()

	sao := proposal.SAO{Signal: proposal.SignalSnapshot{Confidence: 0.9}}
	evidence := []proposal.EvidenceRef{
		{Tool: "metrics", Query: "metrics_q", Live: true},
		{Tool: "loki", Query: "loki_q", Live: true},
	}
	score := func(causal []proposal.CausalScore) float64 {
		set := proposal.Set{
			Proposals:    []proposal.Candidate{{ID: "p1", Confidence: 1, Citations: []string{"metrics_q", "loki_q"}}},
			Evidence:     evidence,
			SAOSnapshot:  &sao,
			CausalScores: causal,
		}
		clank.ScoreConfidencesForTest(&set, sao, nil, "fp", clank.DefaultScoringWeights())
		return set.Proposals[0].Confidence
	}

	noChangeData := score(nil)
	corroborated := score([]proposal.CausalScore{{EventID: "c1", InTopology: true, Likelihood: 0.6}})
	if corroborated <= noChangeData {
		t.Errorf("a run that correlated the incident with a real in-topology change scored %v, no better than the same run holding no change data at all (%v)", corroborated, noChangeData)
	}

	// The out-of-topology case is the floor of the property: an uncorrelatable
	// change must be worth exactly as much as no change, never less.
	elsewhere := score([]proposal.CausalScore{{EventID: "c2", Likelihood: 0.9}})
	if diff := cmp.Diff(noChangeData, elsewhere, cmpopts.EquateApprox(1e-9, 1e-9)); diff != "" {
		t.Error("a change outside the signal's topology must cost nothing against holding no change data (-want +got)\n", diff)
	}
}

// TestScoreConfidences_OnlyInTopologyCausalScoresMoveConfidence pins the
// discrimination the causal term exists to provide. A change somewhere else
// in the cluster is not a weak cause; it is not a cause. If it were allowed
// to multiply in, holding uncorrelatable change data would be strictly worse
// than holding none — and since every score for an out-of-topology target
// caps at the same defence-1 ceiling, the term would read the same number on
// every run and carry no information at all.
func TestScoreConfidences_OnlyInTopologyCausalScoresMoveConfidence(t *testing.T) {
	t.Parallel()

	// Two backends, not two queries: the grounding tier counts distinct
	// EvidenceRef.Tool values, so a pair of refs that named no tool at all
	// would collapse to one source and pull every want below off the
	// GroundingMany tier this table is holding fixed.
	evidence := []proposal.EvidenceRef{
		{Tool: "metrics", Query: "metrics_q", Live: true},
		{Tool: "loki", Query: "loki_q", Live: true},
	}

	// A signal confidence of 0.6 against GroundingMany leaves headroom for the
	// causal bonus to be visible in the wants; at 0.9 every bonus case
	// saturates at 1 and the table stops measuring the thing it names.
	const signalConf, grounding = 0.6, 1.0

	tests := map[string]struct {
		causalScores []proposal.CausalScore
		signalConf   float64 // zero means the shared signalConf above
		selfReported float64 // zero means a ceiling high enough not to bind
		want         float64
	}{
		"scoreConfidences adds a causal bonus when a change event resolved into the topology": {
			causalScores: []proposal.CausalScore{{EventID: "c1", InTopology: true, Likelihood: 0.6}},
			want:         signalConf * grounding * (1 + 0.5*0.6), // raised by Causal * Likelihood
		},
		"scoreConfidences leaves the causal term out entirely when the SAO carried no change events": {
			causalScores: nil,
			want:         signalConf * grounding, // no causal term — LikelihoodOK is false
		},
		"scoreConfidences leaves confidence untouched when every change event landed outside the topology": {
			causalScores: []proposal.CausalScore{{EventID: "c1", Likelihood: 0.5}, {EventID: "c2", Likelihood: 0.5}},
			want:         signalConf * grounding, // identical to carrying no change data at all
		},
		"scoreConfidences ignores a higher out-of-topology Likelihood in favour of the in-topology one": {
			causalScores: []proposal.CausalScore{
				{EventID: "c1", InTopology: true, Likelihood: 0.4},
				{EventID: "c2", Likelihood: 0.9},
			},
			want: signalConf * grounding * (1 + 0.5*0.4), // the max is taken over in-topology scores only, not filtered-then-maxed-over-all
		},
		"scoreConfidences clamps the causal bonus at 1 rather than emitting an impossible confidence": {
			// A self-report above 1 is out of contract and nothing rejects it,
			// so the clamp is the only thing stopping an emitted 1.35.
			causalScores: []proposal.CausalScore{{EventID: "c1", InTopology: true, Likelihood: 1}},
			signalConf:   0.9,
			selfReported: 5,
			want:         1, // 0.9 * 1.0 * (1 + 0.5*1) = 1.35 before the clamp
		},
		"scoreConfidences holds the model's ceiling even when the causal bonus would clear it": {
			causalScores: []proposal.CausalScore{{EventID: "c1", InTopology: true, Likelihood: 1}},
			selfReported: 0.5,
			want:         0.5,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			conf := tc.signalConf
			if conf == 0 {
				conf = signalConf
			}
			selfReported := tc.selfReported
			if selfReported == 0 {
				selfReported = 1
			}
			sao := proposal.SAO{Signal: proposal.SignalSnapshot{Confidence: conf}}
			set := proposal.Set{
				Proposals:    []proposal.Candidate{{ID: "p1", Confidence: selfReported, Citations: []string{"metrics_q", "loki_q"}}},
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
