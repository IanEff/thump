package rattle

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/clank/internal/signal"
)

type Reconciler struct {
	SLOs     []SLO
	Source   Source
	Detector AccelerationDetector
	Debounce *Debouncer
	Now      func() time.Time
}

func (r *Reconciler) Reconcile(ctx context.Context) ([]signal.Detection, error) {
	clock := time.Now
	if r.Now != nil {
		clock = r.Now
	}
	var out []signal.Detection
	for _, slo := range r.SLOs {
		window, err := r.Source.BurnSamples(ctx, slo)
		if err != nil {
			return nil, fmt.Errorf("burn samples for %s: %w", slo.ID, err)
		}
		fired, accel := r.Detector.Detect(window)
		if !fired {
			continue // not accelerating
		}
		now := clock()
		if r.Debounce != nil && !r.Debounce.Allow(fingerprint(slo), now) {
			continue // said it recently — stay quiet
		}
		out = append(out, SignalFor(slo, "burn_rate_acceleration", accel, now))
	}
	return out, nil
}

func fingerprint(slo SLO) string {
	return "slo_burn:" + slo.Object
}
