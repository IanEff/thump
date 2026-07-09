package rattle

import (
	"context"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
)

// Dependency is one node in an SLO's declared upstream/downstream graph —
// what EnrichTopology asks TopologySource about. Role tells a downstream
// reader (never rattle itself) how much a bad state there should matter.
type Dependency struct {
	Name string
	Role string // "blocking" | "optional" | "best-effort"
}

// TopologySource answers "what state is dep in right now?" — the read
// EnrichTopology drives, kept as its own interface so a fake can stand in
// without a live topology resolver.
type TopologySource interface {
	DependencyState(ctx context.Context, dep Dependency) (string, error)
}

// TrafficSample is one affected-percentage observation — the raw material
// EnrichTraffic reduces to a blast-radius figure.
type TrafficSample struct {
	T           time.Time
	AffectedPct float64
}

// TrafficSource supplies the traffic window EnrichTraffic scores — a
// separate query from Source's burn-rate window, since burn rate and
// affected-traffic percentage come from different backends.
type TrafficSource interface {
	TrafficSamples(ctx context.Context, slo SLO) ([]TrafficSample, error)
}

// trajectory turns a burn-rate slope into the direction signal.Divergence and
// EnrichSeverity report: "accelerating" for a positive mean first-difference,
// "recovering" for negative, "stable" for exactly zero.
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

// EnrichTopology appends one ObservedNode per Dependency whose state query
// succeeds. A query error for one dependency is swallowed and that
// dependency skipped — it never fails the whole detection over one bad
// topology read.
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

// EnrichSeverity sets Impact.Severity from the same burn window the firing
// detector already scored: Trajectory always, DegradationPct only when the
// window is non-empty, scaled by how far the latest burn rate runs over the
// SLO's Objective and clamped to 1.0.
func EnrichSeverity(d signal.Detection, burnWindow []Sample, slo SLO) signal.Detection {
	rates := burnRates(burnWindow)
	d.Impact.Severity.Trajectory = trajectory(rates)
	if len(rates) > 0 {
		d.Impact.Severity.DegradationPct = min(rates[len(rates)-1]*(1-slo.Objective), 1.0)
	}
	return d
}

// EnrichTraffic sets Traffic and Impact.BlastRadius from the latest sample in
// traffic. The caller only invokes this when len(traffic) > 0 (see
// Reconciler.Reconcile) — EnrichTraffic itself does not guard against an
// empty slice.
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
