package rattle

import (
	"time"

	"github.com/ianeff/clank/internal/signal"
)

// The window is ignored in v1 (zero-value Divergence). Wave 3 names it and reads
// the acceleration off it to populate Divergence.Observed/Trajectory.
func SignalFor(slo SLO, _ []Sample, now time.Time) signal.Detection {
	return signal.Detection{
		Name:          slo.ID + "-burn-accel",
		Fingerprint:   fingerprint(slo),
		OriginService: slo.Object,
		ServiceTier:   slo.Tier,
		DetectorType:  "burn_rate_acceleration",
		ContractRef:   slo.ContractRef,
		DetectedAt:    now,
	}
}
