package clank

import (
	"math"
	"strings"
	"testing"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRecorder_CountsEveryResultUnderItsOwnLabel pins that partial_non_converging
// gets its own outcome label rather than being folded into (or dropped by)
// success — the belief-formation trap a bare bool couldn't represent.
func TestRecorder_CountsEveryResultUnderItsOwnLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	set := proposal.Set{ServiceTier: "tier-1", FailureClass: proposal.ClassResourceExhaustion}
	r.recordResolution(set, outcome.Outcome{Result: outcome.ResultSuccess})
	r.recordResolution(set, outcome.Outcome{Result: outcome.ResultPartialNonConverging})

	want := `
		# HELP agent_resolutions_total One increment per outcome Click.Absorb accepts.
		# TYPE agent_resolutions_total counter
		agent_resolutions_total{class="resource_exhaustion",intervention="none",outcome="partial_non_converging",tier="tier-1"} 1
		agent_resolutions_total{class="resource_exhaustion",intervention="none",outcome="success",tier="tier-1"} 1
	`
	if err := testutil.CollectAndCompare(reg, strings.NewReader(want), "agent_resolutions_total"); err != nil {
		t.Fatal(err)
	}
}

// TestRecordCalibration_SkipsAppliedAndRendered pins that neither an
// in-flight "applied" nor a dry-run "rendered" outcome moves the
// calibration counter or the confidence histogram — only a terminal
// convergence verdict is ground truth, and the confidence observation sits
// behind that same gate so one proposal is never counted twice across its
// applied-then-converged pair.
func TestRecordCalibration_SkipsAppliedAndRendered(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	set := proposal.Set{
		FailureClass: proposal.ClassDependencySaturation,
		Recommended:  "cand-1",
		Proposals:    []proposal.Candidate{{ID: "cand-1", Confidence: 0.82}},
	}
	r.recordCalibration(set, outcome.Outcome{Result: outcome.ResultApplied})
	r.recordCalibration(set, outcome.Outcome{Result: outcome.ResultRendered})

	if got := testutil.CollectAndCount(r.calibration); got != 0 {
		t.Errorf("calibration counter got %d samples for applied/rendered outcomes, want 0", got)
	}
	if got := testutil.CollectAndCount(r.confidence); got != 0 {
		t.Errorf("confidence histogram got %d samples for applied/rendered outcomes, want 0", got)
	}
}

// TestRecordCalibration_CountsAPartialAsAMiss pins that a
// partial_non_converging verdict scores as a miss under its stated
// confidence bucket, same as failure — a confident model that didn't
// converge is informative, not silence.
func TestRecordCalibration_CountsAPartialAsAMiss(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	set := proposal.Set{
		FailureClass: proposal.ClassDependencySaturation,
		Recommended:  "cand-1",
		Proposals:    []proposal.Candidate{{ID: "cand-1", Confidence: 0.91}},
	}
	r.recordCalibration(set, outcome.Outcome{Result: outcome.ResultPartialNonConverging})
	r.recordCalibration(set, outcome.Outcome{Result: outcome.ResultSuccess})

	want := `
		# HELP agent_proposal_success_total Whether a proposal at a given confidence bucket succeeded.
		# TYPE agent_proposal_success_total counter
		agent_proposal_success_total{confidence_bucket="0.9-1.0",success="false"} 1
		agent_proposal_success_total{confidence_bucket="0.9-1.0",success="true"} 1
	`
	if err := testutil.CollectAndCompare(reg, strings.NewReader(want), "agent_proposal_success_total"); err != nil {
		t.Fatal(err)
	}
}

// TestRecorder_CalibrationScoresSettledResults pins the success path:
// once a result is settled (success/failure), the calibration counter
// records it under the same bucket confidenceBucket assigns.
func TestRecorder_CalibrationScoresSettledResults(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	set := proposal.Set{
		FailureClass: proposal.ClassDependencySaturation,
		Recommended:  "cand-1",
		Proposals:    []proposal.Candidate{{ID: "cand-1", Confidence: 0.82}},
	}
	r.recordCalibration(set, outcome.Outcome{Result: outcome.ResultSuccess})

	want := `
		# HELP agent_proposal_success_total Whether a proposal at a given confidence bucket succeeded.
		# TYPE agent_proposal_success_total counter
		agent_proposal_success_total{confidence_bucket="0.8-0.9",success="true"} 1
	`
	if err := testutil.CollectAndCompare(reg, strings.NewReader(want), "agent_proposal_success_total"); err != nil {
		t.Fatal(err)
	}
}

// TestRecordEffectiveness_ObservesTheForecastError pins the delta itself:
// predicted reduction minus the reduction thump actually measured (pre-action
// severity minus observed severity), both read off the same 0..1
// error-budget axis. A close forecast lands near 0; an action that predicted
// a cut but left severity untouched lands as a large positive.
func TestRecordEffectiveness_ObservesTheForecastError(t *testing.T) {
	set := proposal.Set{
		SAOSnapshot: &proposal.SAO{Signal: proposal.SignalSnapshot{Severity: signal.Severity{DegradationPct: 1.0}}},
		Recommended: "cand-1",
		Proposals:   []proposal.Candidate{{ID: "cand-1", PredictedImpact: &proposal.PredictedImpact{SeverityReductionPct: 0.6}}},
	}

	cases := map[string]struct {
		outcome outcome.Outcome
		want    float64
	}{
		"a converged success near the forecast observes a small delta": {
			outcome: outcome.Outcome{Result: outcome.ResultSuccess, ObservedSeverity: floatPtr(0.1)},
			want:    -0.3, // observedReduction 1.0-0.1=0.9, delta 0.6-0.9
		},
		"a non-converging action that left severity untouched observes a large positive delta": {
			outcome: outcome.Outcome{Result: outcome.ResultPartialNonConverging, ObservedSeverity: floatPtr(1.0)},
			want:    0.6, // observedReduction 1.0-1.0=0, delta 0.6-0
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			r := NewRecorder(reg)
			r.recordEffectiveness(set, tc.outcome)

			count, sum := effectivenessSample(t, reg)
			if count != 1 {
				t.Fatalf("effectiveness histogram got %d samples, want 1", count)
			}
			if math.Abs(sum-tc.want) > 1e-9 {
				t.Errorf("effectiveness delta = %v, want %v", sum, tc.want)
			}
		})
	}
}

// TestRecordEffectiveness_SkipsUnmeasuredAndInterim pins that an outcome
// with no measured severity, or one that hasn't reached a terminal
// convergence verdict, never moves the histogram — treating an unmeasured
// severity as 0 would read as a fabricated full recovery for an action
// nobody actually measured.
func TestRecordEffectiveness_SkipsUnmeasuredAndInterim(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	set := proposal.Set{
		SAOSnapshot: &proposal.SAO{Signal: proposal.SignalSnapshot{Severity: signal.Severity{DegradationPct: 1.0}}},
		Recommended: "cand-1",
		Proposals:   []proposal.Candidate{{ID: "cand-1", PredictedImpact: &proposal.PredictedImpact{SeverityReductionPct: 0.6}}},
	}
	r.recordEffectiveness(set, outcome.Outcome{Result: outcome.ResultSuccess}) // ObservedSeverity nil
	r.recordEffectiveness(set, outcome.Outcome{Result: outcome.ResultApplied, ObservedSeverity: floatPtr(0.1)})
	r.recordEffectiveness(set, outcome.Outcome{Result: outcome.ResultRendered, ObservedSeverity: floatPtr(0.1)})

	if count, _ := effectivenessSample(t, reg); count != 0 {
		t.Errorf("effectiveness histogram got %d samples, want 0", count)
	}
}

// effectivenessSample reads agent_action_effectiveness_delta's sample count
// and sum directly off the registry — it is a plain (unlabeled) Histogram,
// so it always exists as one time series once registered, and
// testutil.CollectAndCount (which counts series, not observations) can't
// distinguish zero observations from one.
func effectivenessSample(t *testing.T, reg *prometheus.Registry) (count uint64, sum float64) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "agent_action_effectiveness_delta" {
			continue
		}
		for _, m := range mf.GetMetric() {
			return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
		}
	}
	t.Fatal("agent_action_effectiveness_delta family not found")
	return 0, 0
}

func floatPtr(f float64) *float64 { return &f }

func TestConfidenceBucket(t *testing.T) {
	tests := []struct {
		conf float64
		want string
	}{
		{0.3, "<0.5"},
		{0.5, "0.5-0.6"},
		{0.82, "0.8-0.9"},
		{0.9, "0.8-0.9"},
		{1.0, "0.9-1.0"},
	}
	for _, tt := range tests {
		if got := confidenceBucket(tt.conf); got != tt.want {
			t.Errorf("confidenceBucket(%v) = %q, want %q", tt.conf, got, tt.want)
		}
	}
}
