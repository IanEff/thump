package clank

import "time"

type ScoringWeights struct {
	Temporal          float64
	Topological       float64
	Historical        float64
	FreshnessHalfLife time.Duration // how fast historical alignment decays by topology staleness (defence 2)
}
