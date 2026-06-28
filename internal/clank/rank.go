package clank

import (
	"cmp"
	"slices"
)

type Ranker struct {
	//
}

func NewRanker() *Ranker {
	return &Ranker{}
}

func (r *Ranker) Rank(cands []Candidate, velocity string) ([]Candidate, RankingRationale) {
	ranked := slices.Clone(cands)
	var why RankingRationale

	if velocity == "accelerating" {
		why.DominantAxis = "time_to_effect"
		why.VelocityWeight = velocity
		slices.SortStableFunc(ranked, func(a, b Candidate) int {
			return cmp.Compare(timeToEffect(a), timeToEffect(b)) // ascending: sooner first
		})
	}

	for i := range ranked {
		ranked[i].Rank = i + 1
	}
	return ranked, why
}

func timeToEffect(c Candidate) int {
	n := 0
	for _, ch := range c.PredictedImpact.SLOEffects["time_to_effect"] {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
