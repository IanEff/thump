package rattle

import (
	"context"
	"time"
)

type Sample struct {
	T        time.Time
	BurnRate float64 // budget consumed / sustainable rate. 1.0 = on pace to exhaust
}

type SLO struct {
	ID           string
	Object       string
	Tier         string
	Objective    float64
	ContractRef  string
	Dependencies []Dependency
}

func (s SLO) AffectedObject() string {
	return s.Object
}

func (s SLO) DeclaredTier() string {
	return s.Tier
}

func (s SLO) Contract() string {
	return s.ContractRef
}

func (s SLO) Kind() string {
	return "slo_burn"
}

type Source interface {
	BurnSamples(ctx context.Context, slo SLO) ([]Sample, error)
}

type AccelerationDetector struct {
	Threshold float64
}

// Detect reports whether the burn is accelerating AND by how much (the mean
// second-difference). Fires was bool-only; Detect keeps that answer and stops
// throwing the magnitude away. The only entry point — callers that only want
// the bool still get it, they just don't get a second name for the same op.
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

type SustainedBurnDetector struct {
	Threshold  float64
	MinSamples int
}

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
