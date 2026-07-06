package rattle

import (
	"time"

	"github.com/ianeff/thump/api/v1/signal"
)

// SignalFor assembles a Detection from evidence the CALLER already computed.
// It never runs a detector itself — a second AccelerationDetector{} silently
// re-deriving accel from the window, in a different file, with a different
// (zero-value) Threshold than the one Reconcile actually fired on, is the bug
// Wave 4.5 retired.
func SignalFor(env Envelope, detectorType string, accel float64, traj string, now time.Time, contract *SignalContract) signal.Detection {
	d := signal.Detection{
		Name:          env.AffectedObject() + "-burn-accel",
		Fingerprint:   fingerprint(env),
		OriginService: env.AffectedObject(),
		ServiceTier:   env.DeclaredTier(),
		DetectorType:  detectorType,
		ContractRef:   env.Contract(),
		DetectedAt:    now,
		Divergence: signal.Divergence{
			Observed:   accel,
			Trajectory: traj,
		},
	}
	if contract != nil {
		d.Divergence.Confidence = contract.Attenuated(1.0, now)
	}
	return d
}
