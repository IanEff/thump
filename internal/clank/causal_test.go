package clank_test

import (
	"testing"
	"time"

	"github.com/ianeff/clank/internal/clank"
)

func TestCausalScorer_TopologyOutweighsRecency(t *testing.T) {
	t.Parallel()
	s := clank.NewCausalScorer()
	change := clank.ChangeSnapshot{
		Events: []clank.ChangeEvent{
			{ID: "old-upstream", Type: "deploy", Target: "payment-gateway", Age: 23 * time.Minute}, // in path
			{ID: "new-unrelated", Type: "deploy", Target: "search-api", Age: 4 * time.Minute},
		},
	}

	got := s.Score(change, topoWithDegradedUpstream("payment-gateway"), heavyTopologyWeights())
	if likelihoodOf(got, "old-upstream") <= likelihoodOf(got, "new-unrelated") {
		t.Errorf("a 23m in-path deploy must outscore a 4m unrelated one\n%+v", got)
	}
}

func TestCausalScorer_HistoricalCannotCarryAHypothesisAlone(t *testing.T) {
	t.Parallel()
	got := clank.NewCausalScorer().Score(historicalMatchNoLiveSource(), anyTopo(), uniformWeights())
	if got[0].Likelihood > 0.5 || got[0].LiveCorroborated {
		t.Errorf("an uncorroborated case-base match must not raise likelihood alone: %+v", got[0])
	}
}

func TestCausalScorer_DecaysHistoricalByTopologyStaleness(t *testing.T) {
	t.Parallel()
	fresh := clank.NewCausalScorer().Score(histMatch(staleness(0)), topo(), uniformWeights())
	stale := clank.NewCausalScorer().Score(histMatch(staleness(90*24*time.Hour)), topo(), uniformWeights())

	if stale[0].Historical >= fresh[0].Historical {
		t.Errorf("case-base alignment must decay as topology goes stale: fresh=%v stale=%v",
			fresh[0].Historical, stale[0].Historical)
	}
}

func TestCausalScorer_AbsentPredictedSignalDecrements(t *testing.T) {
	t.Parallel()
	withPredicted := scoreWhereHypothesisPredicts("db_health_degraded", observed("db_health_degraded"))
	withAbsent := scoreWhereHypothesisPredicts("db_health_degraded", observed("db_health_steady"))
	if withAbsent.Likelihood >= withPredicted.Likelihood {
		t.Errorf("a predicted-but-absent indicator must decrement, not be silent: %v vs %v",
			withAbsent.Likelihood, withPredicted.Likelihood)
	}
}

// topoWithDegradedUpstream returns a topology where the named node is degraded
// in the upstream dependency graph — the "in the blast path" signal.
func topoWithDegradedUpstream(name string) clank.TopologySnapshot {
	return clank.TopologySnapshot{
		Upstream: []clank.NodeState{
			{Name: name, State: "degraded", DegradedSince: 5 * time.Minute, TrafficShare: 0.8},
		},
	}
}

// heavyTopologyWeights returns weights that heavily favor the topological axis
// so an in-path change outscores a more-recent but unrelated one.
func heavyTopologyWeights() clank.CausalWeights {
	return clank.CausalWeights{
		Temporal:          0.1,
		Topological:       0.8,
		Historical:        0.1,
		FreshnessHalfLife: 30 * 24 * time.Hour,
	}
}

// uniformWeights returns equal weights across all three axes —
// no thumb on the scale, useful when the test is about something else.
func uniformWeights() clank.CausalWeights {
	return clank.CausalWeights{
		Temporal:          1.0 / 3,
		Topological:       1.0 / 3,
		Historical:        1.0 / 3,
		FreshnessHalfLife: 30 * 24 * time.Hour,
	}
}

// likelihoodOf finds the CausalScore for the given event ID and returns its
// Likelihood. Panics if the ID isn't in the slice — a test bug, not a scorer bug.
func likelihoodOf(scores []clank.CausalScore, id string) float64 {
	for _, s := range scores {
		if s.EventID == id {
			return s.Likelihood
		}
	}
	panic("no CausalScore for event " + id)
}

// historicalMatchNoLiveSource returns a change snapshot whose target is NOT in
// the topology — so the scorer has a case-base match (historical stub) but no
// live topological corroboration. Defence 1: this alone must not push
// Likelihood above 0.5.
func historicalMatchNoLiveSource() clank.ChangeSnapshot {
	return clank.ChangeSnapshot{
		Events: []clank.ChangeEvent{{
			ID:     "hist-only",
			Type:   "deploy",
			Target: "orphan-service",
			Age:    10 * time.Minute,
		}},
	}
}

// anyTopo returns a topology whose nodes don't match the "orphan-service"
// target from historicalMatchNoLiveSource — so there's no live corroboration.
func anyTopo() clank.TopologySnapshot {
	return clank.TopologySnapshot{
		Upstream: []clank.NodeState{
			{Name: "unrelated-service", State: "healthy", TrafficShare: 0.5},
		},
	}
}

// histMatch returns a change snapshot with a deploy targeting a node that IS in
// the topology (payment-gateway), with the given topology staleness on the
// case-base match. Pair with topo() and uniformWeights().
func histMatch(stale time.Duration) clank.ChangeSnapshot {
	return clank.ChangeSnapshot{
		Events: []clank.ChangeEvent{{
			ID:                  "hist-match",
			Type:                "deploy",
			Target:              "payment-gateway",
			Age:                 10 * time.Minute,
			HistoricalStaleness: stale,
		}},
	}
}

// staleness is a readability alias so the test reads as English:
// histMatch(staleness(90 * 24 * time.Hour)).
func staleness(d time.Duration) time.Duration { return d }

// topo returns a basic topology with payment-gateway degraded upstream — the
// "live" evidence that pairs with histMatch's target.
func topo() clank.TopologySnapshot {
	return clank.TopologySnapshot{
		Upstream: []clank.NodeState{
			{Name: "payment-gateway", State: "degraded", DegradedSince: 5 * time.Minute, TrafficShare: 0.8},
		},
	}
}

// observed is a readability alias so the test reads as English:
// scoreWhereHypothesisPredicts("db_health_degraded", observed("db_health_degraded")).
func observed(state string) string { return state }

// scoreWhereHypothesisPredicts constructs a scenario where a change event
// predicts an indicator and the topology shows the given observed state, then
// scores it and returns the single CausalScore. Defence 3: when observed !=
// predicted, the scorer must decrement Likelihood (absence is evidence against).
func scoreWhereHypothesisPredicts(predicted string, obs string) clank.CausalScore {
	change := clank.ChangeSnapshot{
		Events: []clank.ChangeEvent{{
			ID:               "hyp-event",
			Type:             "deploy",
			Target:           "db-primary",
			Age:              10 * time.Minute,
			PredictedSignals: []string{predicted},
		}},
	}
	t := clank.TopologySnapshot{
		Upstream: []clank.NodeState{{
			Name:          "db-primary",
			State:         obs,
			DegradedSince: 5 * time.Minute,
			TrafficShare:  1.0,
		}},
	}
	scores := clank.NewCausalScorer().Score(change, t, uniformWeights())
	return scores[0]
}
