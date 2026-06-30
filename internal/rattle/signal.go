package rattle

import (
	"time"

	"github.com/ianeff/clank/internal/signal"
)

// SignalFor assembles a Detection from evidence the CALLER already computed.
// It never runs a detector itself — a second AccelerationDetector{} silently
// re-deriving accel from the window, in a different file, with a different
// (zero-value) Threshold than the one Reconcile actually fired on, is the bug
// Wave 4.5 retired.
func SignalFor(slo SLO, detectorType string, accel float64, now time.Time) signal.Detection {
	return signal.Detection{
		Name:          slo.ID + "-burn-accel",
		Fingerprint:   fingerprint(slo),
		OriginService: slo.Object,
		ServiceTier:   slo.Tier,
		DetectorType:  detectorType,
		ContractRef:   slo.ContractRef,
		DetectedAt:    now,
		Divergence: signal.Divergence{
			Observed:   accel,
			Trajectory: "accelerating",
		},
	}
}
