package rattle

import (
	"context"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
)

type Dependency struct {
	Name string
	Role string // "blocking" | "optional" | "best-effort"
}

type TopologySource interface {
	DependencyState(ctx context.Context, dep Dependency) (string, error)
}

type TrafficSample struct {
	T           time.Time
	AffectedPct float64
}

type TrafficSource interface {
	TrafficSamples(ctx context.Context, slo SLO) ([]TrafficSample, error)
}

func trajectory(rates []float64) string {
	switch d := mean(diffs(rates)); {
	case d > 0:
		return "accelerating"
	case d < 0:
		return "recovering"
	default:
		return "stable"
	}
}

func EnrichTopology(ctx context.Context, d signal.Detection, slo SLO, src TopologySource) signal.Detection {
	for _, dep := range slo.Dependencies {
		state, err := src.DependencyState(ctx, dep)
		if err != nil {
			continue
		}
		d.Topology.Upstream = append(d.Topology.Upstream, signal.ObservedNode{Service: dep.Name, State: state})
	}
	return d
}

func EnrichSeverity(d signal.Detection, burnWindow []Sample, slo SLO) signal.Detection {
	rates := burnRates(burnWindow)
	d.Impact.Severity.Trajectory = trajectory(rates)
	if len(rates) > 0 {
		d.Impact.Severity.DegradationPct = min(rates[len(rates)-1]*(1-slo.Objective), 1.0)
	}
	return d
}

func EnrichTraffic(d signal.Detection, traffic []TrafficSample) signal.Detection {
	rates := make([]float64, len(traffic))
	for i, t := range traffic {
		rates[i] = t.AffectedPct
	}
	latest := traffic[len(traffic)-1].AffectedPct
	d.Traffic = signal.TrafficContext{AffectedPct: latest}
	d.Impact.BlastRadius = signal.BlastRadius{AffectedPct: latest, Velocity: trajectory(rates)}
	return d
}
