package rattle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/rattle"
	"github.com/ianeff/clank/internal/signal"
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
	got := rattle.SignalFor(slo, "burn_rate_acceleration", accel, time.Unix(1000, 0), nil)
	got = rattle.EnrichSeverity(got, burn)
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
