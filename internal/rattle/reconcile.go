package rattle

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/signal"
)

type Reconciler struct {
	SLOs              []SLO
	Source            Source
	Detector          AccelerationDetector
	Correlation       *CorrelationDetector
	CorrelationSource MultiSignalSource
	Envelope          *EnvelopeDetector
	BaselineSource    BaselineSource
	Debounce          *Debouncer
	Contract          *SignalContract
	TopologySource    TopologySource
	TrafficSource     TrafficSource
	Now               func() time.Time
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
		now := clock()
		if r.Contract != nil && !r.Contract.Fresh(window, now) {
			continue
		}
		fired, accel := r.Detector.Detect(window)
		detectorType := "burn_rate_acceleration"
		if !fired && r.Correlation != nil && r.CorrelationSource != nil {
			ms, err := r.CorrelationSource.MultiSignals(ctx, slo)
			if err != nil {
				return nil, fmt.Errorf("multi-signals for %s: %w", slo.ID, err)
			}
			if fired = r.Correlation.Fires(ms); fired {
				accel = 0 // CorrelationDetector has no acceleration figure
				detectorType = "multi_signal_correlation"
			}
		}
		if !fired && r.Envelope != nil && r.BaselineSource != nil {
			baseline, err := r.BaselineSource.BaselineSamples(ctx, slo)
			if err != nil {
				return nil, fmt.Errorf("baseline samples for %s: %w", slo.ID, err)
			}
			if fired = r.Envelope.Fires(baseline, window); fired {
				accel = 0 // EnvelopeDetector has no acceleration figure
				detectorType = "historical_envelope_breach"
			}
		}
		if !fired {
			continue
		}
		if r.Debounce != nil && !r.Debounce.Allow(fingerprint(slo), now) {
			continue // said it recently — stay quiet
		}
		d := SignalFor(slo, detectorType, accel, now, r.Contract)
		d = EnrichSeverity(d, window, slo)
		if r.TopologySource != nil {
			d = EnrichTopology(ctx, d, slo, r.TopologySource)
		}
		if r.TrafficSource != nil {
			traffic, err := r.TrafficSource.TrafficSamples(ctx, slo)
			if err != nil {
				return nil, fmt.Errorf("traffic samples for %s: %w", slo.ID, err)
			}
			if len(traffic) > 0 {
				d = EnrichTraffic(d, traffic)
			}
		}
		out = append(out, d)
	}
	return out, nil
}

func fingerprint(env Envelope) string {
	return env.Kind() + ":" + env.AffectedObject()
}

type MultiSignalSource interface {
	MultiSignals(ctx context.Context, slo SLO) (MultiSignalWindow, error)
}

// BaselineSource supplies the historical comparison window EnvelopeDetector
// characterizes as normal — a SEPARATE interface from Source: fetching today's
// samples and fetching the trailing baseline window are different queries.
type BaselineSource interface {
	BaselineSamples(ctx context.Context, slo SLO) ([]Sample, error)
}
