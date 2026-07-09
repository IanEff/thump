package rattle

import (
	"time"

	"github.com/ianeff/thump/api/v1/signal"
)

// SignalFor assembles a signal.Detection from evidence the caller already
// computed — it never runs a detector itself. That's deliberate: a second
// AccelerationDetector{} silently re-deriving accel from the window, with a
// different (zero-value) Threshold than the one that actually fired in
// Reconcile, would report a number nobody chose. SignalFor takes the fired
// value as an argument instead of recomputing it, so there is exactly one
// place accel and trajectory get computed.
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
