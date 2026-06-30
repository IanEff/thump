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
	ID          string
	Object      string
	Tier        string
	Objective   float64
	ContractRef string
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
