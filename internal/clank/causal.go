package clank

import (
	"fmt"
	"math"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
)

// CausalScorer computes, per change event, a Likelihood that the event
// caused the signal — the belief-formation math the reason loop's confidence
// rides on. It never sees the model or the conversation; given the same
// fingerprint, change, topology, and weights it returns the same scores, so
// it's table-tested without a fake Model.
type CausalScorer interface {
	Score(fingerprint string, change proposal.ChangeSnapshot, topo proposal.TopologySnapshot, weights ScoringWeights) []CausalScore
}

// Prior is the scorer's window into the case base — consumer-defined, the
// Go idiom: the interface lives with the code that needs it (Score), and
// CaseBase just happens to satisfy it. A nil Prior means no case base is
// wired yet; Score falls back to the uncorroborated baseline rather than
// panicking.
type Prior interface {
	Alignment(fingerprint string) (float64, bool)
}

// CausalScore lives in api/v1/proposal (hiss's boundary extraction): it
// rides the proposal.Set across to hiss. The scorer that produces it stays
// here.
type CausalScore = proposal.CausalScore

// CausalScorerImpl is the production CausalScorer. Its zero value has a nil
// Prior — belief-formation defence 1 still holds even then, because an
// uncorroborated historical score is capped at historicalAloneCap regardless
// of whether a case base exists to corroborate it.
type CausalScorerImpl struct {
	Prior Prior
}

// NewCausalScorer returns a CausalScorerImpl with no Prior wired; the caller
// sets Prior once a case base exists.
func NewCausalScorer() *CausalScorerImpl {
	return &CausalScorerImpl{}
}

// Score rates every entry in change.Events independently against fingerprint
// and topo — the events are not compared against each other, so a topology
// with two plausible causes returns two scores. Deciding which one wins is
// the Ranker's job, not this one's.
func (s *CausalScorerImpl) Score(fingerprint string, change proposal.ChangeSnapshot, topo proposal.TopologySnapshot, weights ScoringWeights) []CausalScore {
	scores := make([]CausalScore, 0, len(change.Events))
	for _, e := range change.Events {
		scores = append(scores, scoreEvent(fingerprint, e, topo, weights, s.Prior))
	}
	return scores
}

const (
	caseBaseBaseline      = 0.9 // uncorroborated historical baseline, before freshness decay
	historicalAloneCap    = 0.5 // defence 1: historical alignment alone can never clear this likelihood
	negativeSignalPenalty = 0.2 // defence 3: each predicted-but-absent signal costs this much likelihood
)

func scoreEvent(fingerprint string, e proposal.ChangeEvent, topo proposal.TopologySnapshot, weights ScoringWeights, prior Prior) CausalScore {
	node, inPath := findNode(topo, e.Target)

	temporal := temporalScore(e.Age)
	topological := topologicalScore(node, inPath)

	base, corroborated := caseBaseBaseline, false // 0.9 — the uncorroborated stub, unchanged
	if prior != nil {
		if rate, ok := prior.Alignment(fingerprint); ok {
			base, corroborated = rate, true
		}
	}
	historical := base * freshnessFactor(e.HistoricalStaleness, weights.FreshnessHalfLife)

	liveCorroborated := inPath && node.State == "degraded"

	likelihood := weights.Temporal*temporal + weights.Topological*topological + weights.Historical*historical

	priorNote := fmt.Sprintf("historical: case-base prior, staleness %s -> %.2f", e.HistoricalStaleness, historical)
	if corroborated {
		priorNote = fmt.Sprintf("historical: corroborated case base, observed rate %.2f, staleness %s -> %.2f", base, e.HistoricalStaleness, historical)
	}

	rationale := []string{
		fmt.Sprintf("temporal: %s old -> %.2f", e.Age, temporal),
		fmt.Sprintf("topological: in-path=%t -> %.2f", inPath, topological),
		priorNote,
	}

	if !liveCorroborated {
		likelihood = min(likelihood, historicalAloneCap) // historicalAloneCap = 0.5
		rationale = append(rationale, fmt.Sprintf("defence 1: uncorroborated -> capped at %.2f", historicalAloneCap))
	}

	for _, sig := range e.PredictedSignals {
		if !signalObserved(topo, sig) {
			likelihood -= negativeSignalPenalty
		}
		rationale = append(rationale, fmt.Sprintf("defence 3: predicted %q absent -> %.2f", sig, negativeSignalPenalty))
	}

	return CausalScore{
		EventID:          e.ID,
		Temporal:         temporal,
		Topological:      topological,
		Historical:       historical,
		LiveCorroborated: liveCorroborated,
		Likelihood:       clampUnit(likelihood),
		Rationale:        rationale,
	}
}

func findNode(topo proposal.TopologySnapshot, name string) (proposal.NodeState, bool) {
	for _, group := range [][]proposal.NodeState{topo.Upstream, topo.Downstream} {
		for _, n := range group {
			if n.Name == name {
				return n, true
			}
		}
	}
	return proposal.NodeState{}, false
}

const temporalHalfLife = 30 * time.Minute

func temporalScore(age time.Duration) float64 {
	return math.Exp2(-float64(age) / float64(temporalHalfLife))
}

func topologicalScore(node proposal.NodeState, inPath bool) float64 {
	if inPath && node.State == "degraded" {
		return node.TrafficShare
	}
	return 0
}

func freshnessFactor(staleness, halflife time.Duration) float64 {
	if halflife <= 0 {
		return 1 // avoid divide by zero
	}
	return math.Exp2(-float64(staleness) / float64(halflife))
}

func signalObserved(topo proposal.TopologySnapshot, signal string) bool {
	for _, group := range [][]proposal.NodeState{topo.Upstream, topo.Downstream} {
		for _, n := range group {
			if n.State == signal {
				return true
			}
		}
	}
	return false
}

func clampUnit(x float64) float64 {
	return max(0, min(1, x))
}
