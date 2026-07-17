package clank

import (
	"fmt"
	"strconv"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/prometheus/client_golang/prometheus"
)

// confidenceBuckets is shared between the confidence histogram and the
// calibration counter's bucket label — the two series must slice confidence
// identically, or "stated vs. observed" compares two different partitions
// instead of the same one twice.
var confidenceBuckets = []float64{0.5, 0.6, 0.7, 0.8, 0.9, 1.0}

// Recorder is click's Prometheus seam — Absorb calls exactly one method
// here after an outcome clears its audit/coherence checks, so this file is
// the one place that owns the agent_* metric names and label vocabulary.
type Recorder struct {
	resolutions *prometheus.CounterVec
	confidence  *prometheus.HistogramVec // what the model claimed
	calibration *prometheus.CounterVec   // whether it was right

	// effectiveness is agent_action_effectiveness_delta — predicted minus
	// observed severity reduction, both 0..1 on the SLO's error-budget axis.
	// Only a terminal convergence outcome carrying a measured
	// ObservedSeverity contributes a sample; an unmeasured or in-flight
	// outcome is skipped rather than imputed, or a nil would read as a
	// fabricated full recovery.
	effectiveness prometheus.Histogram
}

func NewRecorder(reg prometheus.Registerer) *Recorder {
	r := &Recorder{
		resolutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_resolutions_total",
			Help: "One increment per outcome Click.Absorb accepts.",
		}, []string{"tier", "class", "outcome", "intervention"}),
		confidence: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "agent_proposal_confidence",
			Help:    "Stated hypothesis confidence at propose time.",
			Buckets: confidenceBuckets,
		}, []string{"class"}),
		calibration: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_proposal_success_total",
			Help: "Whether a proposal at a given confidence bucket succeeded.",
		}, []string{"confidence_bucket", "success"}),
		effectiveness: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "agent_action_effectiveness_delta",
			Help:    "Predicted minus observed severity reduction (0..1 error-budget axis); >0 means the model over-predicted a fix.",
			Buckets: prometheus.LinearBuckets(-1, 0.2, 11),
		}),
	}
	reg.MustRegister(r.resolutions, r.confidence, r.calibration, r.effectiveness)
	return r
}

func (r *Recorder) recordResolution(set proposal.Set, o outcome.Outcome) {
	r.resolutions.WithLabelValues(
		set.ServiceTier,
		string(set.FailureClass),
		string(o.Result),
		"none", // no human-intervention concept exists yet
	).Inc()
}

// recordCalibration scores a proposal's stated confidence against ground
// truth. Only a terminal convergence verdict is a calibration sample —
// applied is in-flight, rendered/blocked/unknown never resolved — so the
// confidence histogram is observed behind the same gate as the calibration
// counter, once per proposal, instead of once at every outcome the set
// passes through on its way there.
func (r *Recorder) recordCalibration(set proposal.Set, o outcome.Outcome) {
	if o.Result != outcome.ResultSuccess &&
		o.Result != outcome.ResultFailure &&
		o.Result != outcome.ResultPartialNonConverging {
		return
	}
	conf, ok := recommendedConfidence(set)
	if !ok {
		return // no recommended candidates
	}
	r.confidence.WithLabelValues(string(set.FailureClass)).Observe(conf)

	bucket := confidenceBucket(conf)
	success := strconv.FormatBool(o.Result == outcome.ResultSuccess) // partial and failure both read as a miss
	r.calibration.WithLabelValues(bucket, success).Inc()
}

func recommendedConfidence(set proposal.Set) (float64, bool) {
	for _, cand := range set.Proposals {
		if cand.ID == set.Recommended {
			return cand.Confidence, true
		}
	}
	return 0, false
}

// recordEffectiveness observes agent_action_effectiveness_delta once per
// terminal convergence outcome — how far the ranker's predicted severity
// reduction missed the reduction thump actually measured. Pre-action
// severity is what the reason loop froze into the SAO; post-action severity
// is what the converger read after the window; both sit on the same 0..1
// error-budget axis, so the delta is one honest subtraction. ~0 means the
// model called it; a large positive means it predicted a fix that never
// landed.
func (r *Recorder) recordEffectiveness(set proposal.Set, o outcome.Outcome) {
	if o.Result != outcome.ResultSuccess && o.Result != outcome.ResultPartialNonConverging {
		return // only a terminal convergence outcome carries a measured severity
	}
	if o.ObservedSeverity == nil {
		return // unmeasured — never impute a reduction we didn't observe
	}
	pred, ok := recommendedPrediction(set)
	if !ok {
		return // no forecast to score against
	}
	pre, ok := preSeverity(set)
	if !ok {
		return // no frozen pre-action severity to reduce from
	}
	observedReduction := pre - *o.ObservedSeverity
	r.effectiveness.Observe(pred - observedReduction)
}

// recommendedPrediction returns the recommended candidate's forecast
// severity reduction, mirroring recommendedConfidence's lookup.
func recommendedPrediction(set proposal.Set) (float64, bool) {
	for _, c := range set.Proposals {
		if c.ID == set.Recommended && c.PredictedImpact != nil {
			return c.PredictedImpact.SeverityReductionPct, true
		}
	}
	return 0, false
}

// preSeverity returns the severity the reason loop froze into the SAO — the
// same 0..1 error-budget axis the converger measures post-action on, so the
// two subtract cleanly. A nil snapshot means there is nothing to reduce
// from.
func preSeverity(set proposal.Set) (float64, bool) {
	if set.SAOSnapshot == nil {
		return 0, false
	}
	return set.SAOSnapshot.Signal.Severity.DegradationPct, true
}

// confidenceBucket names which confidenceBuckets interval conf falls in —
// "0.7-0.8" for the half-open (0.7, 0.8], "<0.5" below the lowest boundary.
// Kept in lockstep with the confidence histogram's own Buckets so the two
// series never drift apart into comparing different partitions.
func confidenceBucket(conf float64) string {
	if conf < confidenceBuckets[0] {
		return fmt.Sprintf("<%.1f", confidenceBuckets[0])
	}
	for i := 1; i < len(confidenceBuckets); i++ {
		if conf <= confidenceBuckets[i] {
			return fmt.Sprintf("%.1f-%.1f", confidenceBuckets[i-1], confidenceBuckets[i])
		}
	}
	return fmt.Sprintf(">%.1f", confidenceBuckets[len(confidenceBuckets)-1])
}
