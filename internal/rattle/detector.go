package rattle

import (
	"context"
	"time"
)

// Sample is one burn-rate observation at a point in time — the raw material
// every detector reduces to a fire/no-fire verdict.
type Sample struct {
	T        time.Time
	BurnRate float64 // budget consumed / sustainable rate. 1.0 = on pace to exhaust
}

// SLO is the watched object: one error budget, one dependency graph, one
// action-catalog contract. It implements Envelope so a fingerprint and a
// signal.Detection can be built from it without any detector caring about the
// concrete watched-object type.
type SLO struct {
	ID           string
	Object       string       // the affected object name; the second half of its fingerprint
	Tier         string       // criticality tier, carried through to signal.Detection.ServiceTier — rattle never interprets it
	Objective    float64      // the SLO target (e.g. 0.999), used only to scale DegradationPct in EnrichSeverity
	ContractRef  string       // the authored action-catalog entry this SLO's incidents map to
	Dependencies []Dependency // upstream/downstream nodes EnrichTopology queries for state
}

// AffectedObject satisfies Envelope.
func (s SLO) AffectedObject() string {
	return s.Object
}

// DeclaredTier satisfies Envelope.
func (s SLO) DeclaredTier() string {
	return s.Tier
}

// Contract satisfies Envelope.
func (s SLO) Contract() string {
	return s.ContractRef
}

// Kind satisfies Envelope; "slo_burn" is the fingerprint prefix for every
// SLO-driven detection, regardless of which detector fired.
func (s SLO) Kind() string {
	return "slo_burn"
}

// Source fetches the burn-rate window a detector scores — the one query
// every detector shares, regardless of which one ends up firing.
type Source interface {
	BurnSamples(ctx context.Context, slo SLO) ([]Sample, error)
}

// AccelerationDetector fires when burn rate is not just high but speeding
// up — the second derivative, not the level. Threshold bounds the mean
// second-difference; a bumpy-but-flat burn rate never trips this detector no
// matter how high it runs.
type AccelerationDetector struct {
	Threshold float64
}

// Detect reports whether the burn is accelerating and by how much — the mean
// second-difference, not just a fired bool. This is deliberately the only
// entry point: a second method computing the same magnitude under a
// bool-only name would risk a caller (or a future detector) re-deriving accel
// with different inputs than the ones Detect actually used.
func (d AccelerationDetector) Detect(window []Sample) (fired bool, accel float64) {
	if len(window) < 3 {
		return false, 0
	}
	d1 := diffs(burnRates(window))
	d2 := diffs(d1)
	accel = mean(d2)
	return mean(d1) > 0 && accel > d.Threshold, accel
}

func burnRates(w []Sample) []float64 {
	out := make([]float64, len(w))
	for i, s := range w {
		out[i] = s.BurnRate
	}
	return out
}

func diffs(xs []float64) []float64 {
	if len(xs) < 2 {
		return nil
	}
	out := make([]float64, len(xs)-1)
	for i := range out {
		out[i] = xs[i+1] - xs[i]
	}
	return out
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// SustainedBurnDetector fires when burn rate has held at or above Threshold
// for the trailing MinSamples samples — a level check, complementary to
// AccelerationDetector's slope check. A burn rate that's high but flat never
// trips AccelerationDetector; this is the detector that catches it.
type SustainedBurnDetector struct {
	Threshold  float64
	MinSamples int
}

// Detect reports whether none of the trailing MinSamples samples dropped
// below Threshold, returning the most recent burn rate as level. A window
// shorter than MinSamples, or a zero MinSamples, never fires.
func (d SustainedBurnDetector) Detect(window []Sample) (fired bool, level float64) {
	if len(window) < d.MinSamples || d.MinSamples == 0 {
		return false, 0
	}
	tail := window[len(window)-d.MinSamples:]
	for _, s := range tail {
		if s.BurnRate < d.Threshold {
			return false, 0
		}
	}
	return true, tail[len(tail)-1].BurnRate
}
