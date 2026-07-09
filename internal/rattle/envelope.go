package rattle

import "math"

// EnvelopeDetector fires when the live burn rate exceeds the historical
// baseline mean by more than K standard deviations — an anomaly check
// against the object's own history, distinct from AccelerationDetector's
// slope check and SustainedBurnDetector's fixed level check.
type EnvelopeDetector struct {
	K float64
}

// Fires reports whether live's mean burn rate exceeds baseline's mean by more
// than K standard deviations. Fewer than 2 baseline samples always returns
// false — there's no variance to compare against.
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

// Envelope is what a detector needs from a watched object to fingerprint it
// and populate a signal.Detection — object identity, tier, action-catalog
// contract, and a fingerprint-prefix Kind. SLO is rattle's only
// implementation today, but nothing in this package assumes SLO specifically
// past this interface.
type Envelope interface {
	AffectedObject() string
	DeclaredTier() string
	Contract() string
	Kind() string // fingerprint prefix
}

var _ Envelope = SLO{}
