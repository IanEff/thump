package clank

import (
	"cmp"
	"slices"

	"github.com/ianeff/thump/api/v1/proposal"
)

type Ranker struct {
	//
}

func NewRanker() *Ranker {
	return &Ranker{}
}

func (r *Ranker) Rank(cands []proposal.Candidate, velocity string) ([]proposal.Candidate, proposal.RankingRationale) {
	ranked := slices.Clone(cands)
	var why proposal.RankingRationale

	if velocity == "accelerating" {
		why.DominantAxis = "time_to_effect"
		why.VelocityWeight = velocity
		slices.SortStableFunc(ranked, func(a, b proposal.Candidate) int {
			return cmp.Compare(timeToEffect(a), timeToEffect(b)) // ascending: sooner first
		})
	}

	for i := range ranked {
		ranked[i].Rank = i + 1
	}
	return ranked, why
}

func timeToEffect(c proposal.Candidate) int {
	if c.PredictedImpact == nil {
		return 0
	}
	n := 0
	for _, ch := range c.PredictedImpact.SLOEffects["time_to_effect"] {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
