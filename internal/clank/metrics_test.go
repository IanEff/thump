package clank

import (
	"strings"
	"testing"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
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

// TestRecorder_CalibrationSkipsUnsettledResults pins that a dry-run
// "rendered" outcome moves the confidence histogram (a stated belief was
// formed) but never the calibration counter (there is no ground truth yet
// to score it against) — recordCalibration must not treat a non-answer as
// evidence either way.
func TestRecorder_CalibrationSkipsUnsettledResults(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	set := proposal.Set{
		FailureClass: proposal.ClassDependencySaturation,
		Recommended:  "cand-1",
		Proposals:    []proposal.Candidate{{ID: "cand-1", Confidence: 0.82}},
	}
	r.recordCalibration(set, outcome.Outcome{Result: outcome.ResultRendered})

	if got := testutil.CollectAndCount(r.calibration); got != 0 {
		t.Fatalf("calibration counter got %d samples for an unsettled result, want 0", got)
	}
	if got := testutil.CollectAndCount(r.confidence); got != 1 {
		t.Fatalf("confidence histogram got %d samples, want 1 (a belief was still stated)", got)
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
