package rattle

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
)

// Reconciler runs every watched SLO through its detectors OR'd together —
// acceleration, then sustained burn, then correlation, then historical
// envelope — stopping at the first one that fires, then enriching and
// debouncing the result. Every optional field (Correlation, Envelope,
// TopologySource, TrafficSource, Debounce, Contract) is nil-safe: a
// Reconciler with only SLOs, Source, and Detector set still runs, just
// without that branch or enrichment step.
type Reconciler struct {
	SLOs              []SLO
	Source            Source                 // the shared burn-window fetch every detector scores
	Detector          AccelerationDetector   // always runs first
	Correlation       *CorrelationDetector   // nil disables the correlation branch
	CorrelationSource MultiSignalSource      // required alongside Correlation
	Envelope          *EnvelopeDetector      // nil disables the historical-envelope branch
	BaselineSource    BaselineSource         // required alongside Envelope
	Sustained         *SustainedBurnDetector // nil disables the sustained-burn branch
	Debounce          *Debouncer             // nil disables debouncing entirely
	Contract          *SignalContract        // nil skips the freshness gate and confidence attenuation
	TopologySource    TopologySource         // nil skips EnrichTopology
	TrafficSource     TrafficSource          // nil skips EnrichTraffic
	Now               func() time.Time       // nil defaults to time.Now; overridden in tests for determinism
}

// Reconcile runs one pass over every watched SLO: fetch its burn window, gate
// it on freshness, run the detectors in order until one fires, debounce,
// enrich, and collect the result. A source or query error for any one SLO
// aborts the whole pass — Reconcile returns the error and no detections at
// all, never a partial slice alongside the error.
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

		if !fired && r.Sustained != nil {
			var level float64
			if fired, level = r.Sustained.Detect(window); fired {
				accel = level
				detectorType = "sustained_burn"
			}
		}
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
		traj := trajectory(burnRates(window))
		d := SignalFor(slo, detectorType, accel, traj, now, r.Contract)
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

// fingerprint is the dedup key every Detector-driven signal shares: kind
// (the detector-agnostic object category) plus the affected object name.
func fingerprint(env Envelope) string {
	return env.Kind() + ":" + env.AffectedObject()
}

// MultiSignalSource supplies the per-metric windows CorrelationDetector scores
// together — a separate query from Source's single burn-rate window, since
// correlation needs several series, not one.
type MultiSignalSource interface {
	MultiSignals(ctx context.Context, slo SLO) (MultiSignalWindow, error)
}

// BaselineSource supplies the historical comparison window EnvelopeDetector
// characterizes as normal — a SEPARATE interface from Source: fetching today's
// samples and fetching the trailing baseline window are different queries.
type BaselineSource interface {
	BaselineSamples(ctx context.Context, slo SLO) ([]Sample, error)
}
