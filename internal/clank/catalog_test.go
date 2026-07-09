package clank_test

import (
	"testing"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/contract"
)

func TestCatalog(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		sao      proposal.SAO
		contract string // the contract whose presence we assert on
		want     bool   // should it be in the applicable set?
	}{
		"Catalog returns a contract applicable to the class and tier under blast": {
			sao:      saoWithAffectedPct(12),
			contract: "throttle-non-critical-paths",
			want:     true,
		},
		"Catalog drops a contract failing its amplification precondition": {
			sao:      saoWithSharedPoolBottleneck(),
			contract: "scale-out",
			want:     false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cat := contract.NewStaticCatalog(testContracts())
			got := cat.Applicable(proposal.ClassDependencySaturation, "tier-1", tc.sao)
			if has := containsContract(got, tc.contract); has != tc.want {
				t.Errorf("contract %q applicable=%v, want %v; applicable set: %v",
					tc.contract, has, tc.want, names(got))
			}
		})
	}
}

func testContracts() []contract.ActionContract {
	return []contract.ActionContract{
		{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1", "tier-2"},
			Preconditions: []contract.Precondition{
				{Name: "affected_pct_under_50", OK: func(sao proposal.SAO) bool {
					return sao.Signal.BlastRadius.AffectedPct < 50
				}},
			},
		},
		{
			Name:                     "scale-out",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Preconditions: []contract.Precondition{
				{Name: "not_shared_pool_bottleneck", OK: func(sao proposal.SAO) bool {
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

func saoWithAffectedPct(p float64) proposal.SAO {
	return proposal.SAO{
		Signal: proposal.SignalSnapshot{
			BlastRadius: signal.BlastRadius{AffectedPct: p},
		},
	}
}

func saoWithSharedPoolBottleneck() proposal.SAO {
	return proposal.SAO{
		Signal: proposal.SignalSnapshot{
			BlastRadius: signal.BlastRadius{AffectedPct: 10},
		},
		Topology: proposal.TopologySnapshot{
			Upstream: []proposal.NodeState{
				{Name: "db-pool", State: "shared_connection_pool_bottleneck"},
			},
		},
	}
}

func containsContract(cs []contract.ActionContract, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func names(cs []contract.ActionContract) []string {
	var names []string
	for _, c := range cs {
		names = append(names, c.Name)
	}
	return names
}
