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
}
