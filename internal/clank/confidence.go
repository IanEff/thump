package clank

import "github.com/ianeff/thump/api/v1/proposal"

// confidenceInputs bundles what scoreConfidence needs to grade one
// Candidate — every field already lives on the audit trail.
type confidenceInputs struct {
	SignalConfidence float64 // SignalSnapshot.Confidence — rattle's number, read-only
	Corroborated     int     // this candidate's Citations resolving to a Live, in-topology EvidenceRef
	Alignment        float64 // Prior.Alignment's rate — meaningless unless AlignmentOK
	AlignmentOK      bool    // true once the case base clears its own ≥2-vote floor (defence 1)
	Likelihood       float64 // the strongest CausalScore.Likelihood this run produced — meaningless unless LikelihoodOK
	LikelihoodOK     bool    // true only when the SAO had change events to score at all
	SelfReported     float64 // the model's own stated confidence
}

// scoreConfidence computes a Candidate's emitted confidence as a product of
// what this run actually grounded, then applies the model's self-report as
// a ceiling — min(computed, in.SelfReported) — so a confident-sounding guess
// with nothing behind it can only be pulled down, never talked up. A term
// whose *OK flag is false drops out of the product entirely; it never
// multiplies in as zero.
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
		computed *= in.Likelihood
	}

	return min(computed, in.SelfReported)
}

// coherentLiveCitations counts how many of cand's Citations resolve to an
// EvidenceRef that is both Live and topologically coherent — the same test
// gate.go's anyCoherentLive applies, counted here instead of just asked
// yes/no, since scoreConfidence's grounding tiers care how many, not
// whether any.
func coherentLiveCitations(cand proposal.Candidate, evidence []proposal.EvidenceRef, sao *proposal.SAO) int {
	cited := make(map[string]bool, len(cand.Citations))
	for _, c := range cand.Citations {
		cited[c] = true
	}

	n := 0
	for _, ref := range evidence {
		if cited[ref.Query] && ref.Live && coherentSubject(ref, sao) {
			n++
		}
	}
	return n
}

// scoreConfidences overwrites every set.Proposals entry's Confidence with
// scoreConfidence's output — each candidate graded on its own citations, so
// two candidates in the same set can end up with different grounding. The
// causal term is shared across candidates (it describes the run's change
// events, not any one action) and is present only when sao.Change had
// events to score; the corroboration term is per-candidate.
func scoreConfidences(set *proposal.Set, sao proposal.SAO, prior Prior, fingerprint string, w ScoringWeights) {
	maxLikelihood, likelihoodOK := 0.0, len(set.CausalScores) > 0
	for _, cs := range set.CausalScores {
		maxLikelihood = max(maxLikelihood, cs.Likelihood)
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
