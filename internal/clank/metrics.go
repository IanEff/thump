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

	// effectiveness is registered but never observed. Action Effectiveness
	// needs a measured post-action severity — Outcome (api/v1/outcome) has
	// no field for it yet; only rattle observes raw signal severity, and
	// nothing re-checks it after thump acts. The name exists so a dashboard
	// or the next PR has somewhere to point; until it's wired, this series
	// reads as a permanent, empty histogram — not "zero effectiveness."
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
			Help:    "Parked: predicted vs. observed severity reduction. Not yet wired — Outcome carries no measured-impact field.",
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

func (r *Recorder) recordCalibration(set proposal.Set, o outcome.Outcome) {
	conf, ok := recommendedConfidence(set)
	if !ok {
		return // no recommended candidates
	}
	class := string(set.FailureClass)
	r.confidence.WithLabelValues(class).Observe(conf)

	if o.Result != outcome.ResultSuccess && o.Result != outcome.ResultFailure {
		return
	}
	bucket := confidenceBucket(conf)
	success := strconv.FormatBool(o.Result == outcome.ResultSuccess)
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
