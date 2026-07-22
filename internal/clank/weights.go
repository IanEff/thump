package clank

import "time"

// ScoringWeights tunes CausalScorerImpl.Score — how much temporal recency,
// topological proximity, and historical case-base alignment each contribute
// to a CausalScore.Likelihood. These are scorer tuning, not policy: unlike
// hiss's confidence floors, they shape how the scorer weighs evidence, not
// what the system is allowed to act on, which is why they stay in clank.
type ScoringWeights struct {
	Temporal          float64
	Topological       float64
	Historical        float64
	FreshnessHalfLife time.Duration // how fast historical alignment decays by topology staleness (defence 2)

	// GroundingNone, GroundingOne, and GroundingMany are scoreConfidence's
	// multiplier for a candidate whose citations resolve to 0, 1, or 2+
	// live, in-topology EvidenceRefs — the same tiered floor causal.go
	// already applies to Likelihood, applied here to emitted confidence.
	GroundingNone float64
	GroundingOne  float64
	GroundingMany float64
}
