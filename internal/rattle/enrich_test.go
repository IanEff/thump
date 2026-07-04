package rattle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/ianeff/thump/internal/rattle"
	"github.com/ianeff/thump/internal/signal"
)

func TestEnrichTopology_PopulatesObservedDependencyState(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{
		ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Dependencies: []rattle.Dependency{{Name: "payment-gateway", Role: "blocking"}},
	}
	topo := fakeTopologySource{"payment-gateway": "degraded"}

	got := rattle.EnrichTopology(context.Background(), signal.Detection{}, slo, topo)

	want := signal.TopologyContext{
		Upstream: []signal.ObservedNode{{Service: "payment-gateway", State: "degraded"}},
	}
	if diff := cmp.Diff(want, got.Topology); diff != "" {
		t.Error("wrong observed topology state", diff)
	}
}

func TestEnrich_BlastRadiusVelocityIsIndependentOfSeverityTrajectory(t *testing.T) {
	t.Parallel()
	burn := window(2, 2, 2, 2)                       // flat - severity trajectory "stable"
	traffic := trafficWindow(0.05, 0.06, 0.09, 0.15) // accelerating share
	_, accel := rattle.AccelerationDetector{}.Detect(burn)

	slo := rattle.SLO{Object: "ceph-rgw", Tier: "tier-1"}
	got := rattle.SignalFor(slo, "burn_rate_acceleration", accel, "accelerating", time.Unix(1000, 0), nil)
	got = rattle.EnrichSeverity(got, burn, slo)
	got = rattle.EnrichTraffic(got, traffic)

	if got.Impact.Severity.Trajectory != "stable" {
		t.Fatal("test fixture broken: a flat burn window must read as stable", got.Impact.Severity.Trajectory)
	}
	if got.Impact.BlastRadius.Velocity != "accelerating" {
		t.Error("blast radius should read its own derivative off the traffic window, not the burn window", cmp.Diff("accelerating", got.Impact.BlastRadius.Velocity))
	}
}

type fakeTopologySource map[string]string

func (f fakeTopologySource) DependencyState(_ context.Context, dep rattle.Dependency) (string, error) {
	return f[dep.Name], nil
}

func trafficWindow(pcts ...float64) []rattle.TrafficSample {
	out := make([]rattle.TrafficSample, len(pcts))
	base := time.Unix(0, 0)
	for i, p := range pcts {
		out[i] = rattle.TrafficSample{T: base.Add(time.Duration(i) * time.Minute), AffectedPct: p}
	}
	return out
}

func TestEnrichSeverity_CalculatesDegradationPctAndTrajectoryFromObjectiveAndBurnRates(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		window []rattle.Sample
		slo    rattle.SLO
		want   signal.Severity
	}{
		"EnrichSeverity sets degradation to 100 percent for ceph-health pinned at burn ceiling": {
			window: window(1000),
			slo: rattle.SLO{
				Objective: 0.999,
			},
			want: signal.Severity{
				Trajectory:     "stable",
				DegradationPct: 1.0,
			},
		},
		"EnrichSeverity calculates correct degradation pct for moderate osd burn rate": {
			window: window(1.267),
			slo: rattle.SLO{
				Objective: 0.99,
			},
			want: signal.Severity{
				Trajectory:     "stable",
				DegradationPct: 0.01267,
			},
		},
		"EnrichSeverity clamps degradation pct to 1.0 for burn rate past the ceiling": {
			window: window(2000),
			slo: rattle.SLO{
				Objective: 0.99,
			},
			want: signal.Severity{
				Trajectory:     "stable",
				DegradationPct: 1.0,
			},
		},
		"EnrichSeverity does not panic and keeps degradation at zero for empty burn window": {
			window: nil,
			slo: rattle.SLO{
				Objective: 0.99,
			},
			want: signal.Severity{
				Trajectory:     "stable",
				DegradationPct: 0.0,
			},
		},
		"EnrichSeverity sets trajectory to accelerating for climbing burn rate": {
			window: window(1.0, 2.0, 4.0, 8.0),
			slo: rattle.SLO{
				Objective: 0.99,
			},
			want: signal.Severity{
				Trajectory:     "accelerating",
				DegradationPct: 0.08,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := rattle.EnrichSeverity(signal.Detection{}, tc.window, tc.slo)
			if diff := cmp.Diff(tc.want, got.Impact.Severity, cmpopts.EquateApprox(1e-9, 1e-9)); diff != "" {
				t.Error("wrong severity enrichment result", diff)
			}
		})
	}
}
