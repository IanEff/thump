package clank

import (
	"cmp"
	"slices"

	"github.com/ianeff/thump/api/v1/proposal"
)

// Ranker orders a proposal.Set's candidates deterministically and
// auditably — never a black box. Given the same candidates and velocity it
// always returns the same order and proposal.RankingRationale, so it's
// table-tested without a fake Model.
type Ranker struct {
	//
}

// NewRanker returns a Ranker ready to use; it holds no state.
func NewRanker() *Ranker {
	return &Ranker{}
}

// Rank orders cands by time-to-effect, soonest first, when velocity is
// "accelerating"; otherwise it leaves cands in their given order. velocity is
// the signal's blast-radius velocity, not its severity — the two impact axes
// are never collapsed into one score here. Every returned candidate's Rank
// field is set to its 1-based position in the result.
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
