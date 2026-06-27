package clank_test

import (
	"testing"

	"github.com/ianeff/clank/internal/clank"
)

func TestCatalog_ReturnsOnlyAppplicableContracts(t *testing.T) {
	t.Parallel()
	cat := clank.NewStaticCatalog(testContracts())
	got := cat.Applicable(clank.ClassDependencySaturation, "tier-1", saoWithAffectedPct(12))
	if !containsContract(got, "throttle-non-critical-paths") {
		t.Errorf("throttle applies to dependency_saturation/tier-1 at !@%% blast: %v", names(got))
	}
}

func TestCatalog_DropsContractsFailingAmplificationPrecondition(t *testing.T) {
	cat := clank.NewStaticCatalog(testContracts())
	got := cat.Applicable(clank.ClassDependencySaturation, "tier-1", saoWithSharedPoolBottleneck())
	if containsContract(got, "scale-out") {
		t.Errorf("scale_out must drop when bottleneck == shared_connection_pool: %v", names(got))
	}
}

func testContracts() []clank.ActionContract {
	return []clank.ActionContract{
		{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []clank.FailureClass{clank.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1", "tier-2"},
			Preconditions: []clank.Precondition{
				{Name: "affected_pct_under_50", OK: func(sao clank.SAO) bool {
					return sao.Signal.BlastRadius.AffectedPct < 50
				}},
			},
		},
		{
			Name:                     "scale-out",
			ApplicableFailureClasses: []clank.FailureClass{clank.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Preconditions: []clank.Precondition{
				{Name: "not_shared_pool_bottleneck", OK: func(sao clank.SAO) bool {
					for _, n := range sao.Topology.Upstream {
						if n.State == "shared_connection_pool_bottleneck" {
							return false
						}
					}
					return true
				}},
			},
		},
	}
}

func saoWithAffectedPct(p float64) clank.SAO {
	return clank.SAO{
		Signal: clank.SignalSnapshot{
			BlastRadius: clank.BlastRadius{AffectedPct: p},
		},
	}
}

func saoWithSharedPoolBottleneck() clank.SAO {
	return clank.SAO{
		Signal: clank.SignalSnapshot{
			BlastRadius: clank.BlastRadius{AffectedPct: 10},
		},
		Topology: clank.TopologySnapshot{
			Upstream: []clank.NodeState{
				{Name: "db-pool", State: "shared_connection_pool_bottleneck"},
			},
		},
	}
}

func containsContract(cs []clank.ActionContract, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func names(cs []clank.ActionContract) []string {
	var names []string
	for _, c := range cs {
		names = append(names, c.Name)
	}
	return names
}
