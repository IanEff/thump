package clank

import (
	"fmt"
	"math"
	"time"

	"github.com/ianeff/thump/internal/proposal"
)

type CausalScorer interface {
	Score(fingerprint string, change ChangeSnapshot, topo TopologySnapshot, weights ScoringWeights) []CausalScore
}

// Prior is the scorer's window into the case base — consumer-defined, the
// Go idiom: the interface lives with the code that needs it, and CaseBase
// just happens to satisfy it. nil means "no case base yet": the stub
// constant, exactly as before.
type Prior interface {
	Alignment(fingerprint string) (float64, bool)
}

// CausalScore moved to internal/proposal (hiss Wave 1): it rides the
// ProposalSet across the boundary. The scorer that produces it stays here.
type CausalScore = proposal.CausalScore

type CausalScorerImpl struct {
	Prior Prior
}

func NewCausalScorer() *CausalScorerImpl {
	return &CausalScorerImpl{}
}

func (s *CausalScorerImpl) Score(fingerprint string, change ChangeSnapshot, topo TopologySnapshot, weights ScoringWeights) []CausalScore {
	scores := make([]CausalScore, 0, len(change.Events))
	for _, e := range change.Events {
		scores = append(scores, scoreEvent(fingerprint, e, topo, weights, s.Prior))
	}
	return scores
}

const (
	caseBaseBaseline      = 0.9
	historicalAloneCap    = 0.5
	negativeSignalPenalty = 0.2
)

func scoreEvent(fingerprint string, e ChangeEvent, topo TopologySnapshot, weights ScoringWeights, prior Prior) CausalScore {
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

func findNode(topo TopologySnapshot, name string) (NodeState, bool) {
	for _, group := range [][]NodeState{topo.Upstream, topo.Downstream} {
		for _, n := range group {
			if n.Name == name {
				return n, true
			}
		}
	}
	return NodeState{}, false
}

const temporalHalfLife = 30 * time.Minute

func temporalScore(age time.Duration) float64 {
	return math.Exp2(-float64(age) / float64(temporalHalfLife))
}

func topologicalScore(node NodeState, inPath bool) float64 {
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

func signalObserved(topo TopologySnapshot, signal string) bool {
	for _, group := range [][]NodeState{topo.Upstream, topo.Downstream} {
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
