package rattle

import "math"

type EnvelopeDetector struct {
	K float64
}

func (d EnvelopeDetector) Fires(baseline, live []Sample) bool {
	if len(baseline) < 2 {
		return false // math won't math with <2 points
	}
	bMean := mean(burnRates(baseline))
	bStdDev := stddev(burnRates(baseline), bMean)
	lMean := mean(burnRates(live))
	return lMean > bMean+d.K*bStdDev
}

func stddev(xs []float64, m float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sumSq float64
	for _, x := range xs {
		sumSq += (x - m) * (x - m)
	}
	return math.Sqrt(sumSq / float64(len(xs)))
}
