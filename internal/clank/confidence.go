package clank

import "github.com/ianeff/thump/api/v1/proposal"

// confidenceInputs bundles what scoreConfidence needs to grade one
// Candidate — every field already lives on the audit trail.
type confidenceInputs struct {
	SignalConfidence float64 // SignalSnapshot.Confidence — rattle's number, read-only
	Corroborated     int     // this candidate's distinct backends resolving to a Live, in-topology EvidenceRef
	Alignment        float64 // Prior.Alignment's rate — meaningless unless AlignmentOK
	AlignmentOK      bool    // true once the case base clears its own ≥2-vote floor (defence 1)
	Likelihood       float64 // the strongest in-topology CausalScore.Likelihood this run produced — meaningless unless LikelihoodOK
	LikelihoodOK     bool    // true only when at least one change event resolved into the signal's own topology
	SelfReported     float64 // the model's own stated confidence
}

// scoreConfidence computes a Candidate's emitted confidence from what this run
// actually grounded, then applies the model's self-report as a ceiling —
// min(computed, in.SelfReported) — so a confident-sounding guess with nothing
// behind it can only be pulled down, never talked up. A term whose *OK flag is
// false drops out entirely; it never multiplies in as zero.
//
// The causal term is added rather than multiplied. LikelihoodOK is false
// whenever no change event resolved into the topology, so as a factor it
// scored a run holding corroborating change data below the identical run
// holding none — confidence falling as evidence arrives.
func scoreConfidence(in confidenceInputs, w ScoringWeights) float64 {
	grounding := w.GroundingNone
	switch {
	case in.Corroborated >= 2:
		grounding = w.GroundingMany
	case in.Corroborated == 1:
		grounding = w.GroundingOne
	}

	computed := in.SignalConfidence * grounding
	if in.AlignmentOK {
		computed *= 0.5 + 0.5*in.Alignment
	}
	if in.LikelihoodOK {
		computed = min(1, computed*(1+w.Causal*in.Likelihood))
	}

	return min(computed, in.SelfReported)
}

// coherentLiveCitations counts the distinct backends behind cand's
// Citations that resolve to a Live, topologically coherent EvidenceRef —
// grouped by EvidenceRef.Tool, not one per ref, so a candidate can't clear
// the ≥2-source grounding floor by querying one backend twice (defence 1).
// The same test gate.go's anyCoherentLive applies, counted here instead of
// just asked yes/no.
func coherentLiveCitations(cand proposal.Candidate, evidence []proposal.EvidenceRef, sao *proposal.SAO) int {
	cited := make(map[string]bool, len(cand.Citations))
	for _, c := range cand.Citations {
		cited[c] = true
	}

	backends := make(map[string]bool)
	for _, ref := range evidence {
		if cited[ref.Query] && ref.Live && coherentSubject(ref, sao) {
			backends[ref.Tool] = true
		}
	}
	return len(backends)
}

// scoreConfidences overwrites every set.Proposals entry's Confidence with
// scoreConfidence's output — each candidate graded on its own citations, so
// two candidates in the same set can end up with different grounding. The
// causal term is shared across candidates (it describes the run's change
// events, not any one action); the corroboration term is per-candidate.
//
// Only scores whose event resolved into the signal's topology may contribute:
// a change somewhere else in the cluster is not a weak cause, it is not a
// cause, and letting it count would make holding uncorrelatable change data
// worse than holding none — the same trap defence 3 avoids by decrementing on
// absent predicted signals rather than on silence.
func scoreConfidences(set *proposal.Set, sao proposal.SAO, prior Prior, fingerprint string, w ScoringWeights) {
	var maxLikelihood float64
	var likelihoodOK bool
	for _, cs := range set.CausalScores {
		if !cs.InTopology {
			continue
		}
		maxLikelihood, likelihoodOK = max(maxLikelihood, cs.Likelihood), true
	}

	var alignment float64
	var alignmentOK bool
	if prior != nil {
		alignment, alignmentOK = prior.Alignment(fingerprint)
	}

	for i := range set.Proposals {
		cand := &set.Proposals[i]
		cand.Confidence = scoreConfidence(confidenceInputs{
			SignalConfidence: sao.Signal.Confidence,
			Corroborated:     coherentLiveCitations(*cand, set.Evidence, set.SAOSnapshot),
			Alignment:        alignment,
			AlignmentOK:      alignmentOK,
			Likelihood:       maxLikelihood,
			LikelihoodOK:     likelihoodOK,
			SelfReported:     cand.Confidence,
		}, w)
	}
}
