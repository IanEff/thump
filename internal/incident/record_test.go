package incident_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/incident"
)

// TestFoldRecord_ARecordCarriesBothConfidenceNumbersAndTheReasonTheGateFired
// pins what phase AV could not see about itself: hiss's escalation reason
// distinguishes ReasonGroundingFloor from ReasonConfidenceFloor
// (internal/hiss/authority.go:69-76) and the Candidate carries Confidence and
// ComputedConfidence separately (internal/clank/confidence.go:77-80), but the
// fold kept only Governed, so "which number bound this decision" was
// unanswerable from the read model for the entire phase that existed to
// change it.
func TestFoldRecord_ARecordCarriesBothConfidenceNumbersAndTheReasonTheGateFired(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		emitted     float64
		computed    float64
		reasons     []string
		wantReasons []string
	}{
		"a well-grounded candidate a cautious model hedged on records the self-report as the binding term": {
			emitted:     0.65,
			computed:    1.00,
			reasons:     []string{decision.ReasonConfidenceFloor},
			wantReasons: []string{decision.ReasonConfidenceFloor},
		},
		"a thinly-grounded candidate records the grounding floor as the binding term under the split gate": {
			emitted:     0.95,
			computed:    0.375,
			reasons:     []string{decision.ReasonGroundingFloor},
			wantReasons: []string{decision.ReasonGroundingFloor},
		},
		"a candidate that cleared both floors records no reason at all, which is how the artifact shows the gate was never the constraint": {
			emitted:     0.85,
			computed:    1.00,
			reasons:     nil,
			wantReasons: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			set := proposal.Set{
				SignalRef:   "fp-1",
				Recommended: "cand-1",
				Proposals: []proposal.Candidate{
					{
						ID:                 "cand-1",
						Confidence:         tc.emitted,
						ComputedConfidence: tc.computed,
					},
				},
			}
			gov := decision.Governed{
				Decision: decision.Decision{
					SignalRef:    "fp-1",
					CandidateRef: "cand-1",
					Verdict:      decision.VerdictApproved,
					Reasons:      tc.reasons,
				},
				Set: set,
			}
			if len(tc.reasons) > 0 {
				gov.Decision.Verdict = decision.VerdictEscalate
			}

			rec := incident.FoldRecord(incident.Record{}, set)
			rec = incident.FoldRecord(rec, gov)

			if rec.Proposed == nil {
				t.Fatal("want Proposed populated on Record, got nil")
			}
			if rec.Decided == nil {
				t.Fatal("want Decided populated on Record, got nil")
			}

			if diff := cmp.Diff(tc.wantReasons, rec.Decided.Reasons); diff != "" {
				t.Error("wrong decision reasons on record", diff)
			}
			if diff := cmp.Diff(tc.emitted, rec.Proposed.Proposals[0].Confidence); diff != "" {
				t.Error("wrong emitted confidence on record", diff)
			}
			if diff := cmp.Diff(tc.computed, rec.Proposed.Proposals[0].ComputedConfidence); diff != "" {
				t.Error("wrong computed confidence on record", diff)
			}
		})
	}
}

// TestFoldRecord_ARejectedCandidateSurvivesIntoTheRecord pins the charter's
// "the set is the audit unit": clank emits the whole ranked proposal.Set and
// Fold kept none of it, so the read model could say what was done and never
// what was considered and declined — which is most of what a reviewer wants.
func TestFoldRecord_ARejectedCandidateSurvivesIntoTheRecord(t *testing.T) {
	t.Parallel()

	set := proposal.Set{
		SignalRef:   "fp-1",
		Recommended: "cand-1",
		Proposals: []proposal.Candidate{
			{ID: "cand-1", ContractRef: "restart-service", Rank: 1},
			{ID: "cand-2", ContractRef: "scale-deployment", Rank: 2},
			{ID: "cand-3", ContractRef: "shed-load", Rank: 3},
		},
	}

	rec := incident.FoldRecord(incident.Record{}, set)

	if rec.Proposed == nil {
		t.Fatal("want Proposed populated on Record, got nil")
	}
	if diff := cmp.Diff(set.Proposals, rec.Proposed.Proposals); diff != "" {
		t.Error("wrong candidates preserved in record", diff)
	}
}

// TestFoldRecord_AnUndoThatWasHeldIsDistinguishableFromAnUndoThatRan pins the
// distinction D-30's argument turns on. Reversal.HoldOnMiss, authored true on
// both flagd actions (config/dev/actions/catalog.yaml:133, :162), makes
// ReversalWatcher.Watch set fire=false and Held=true
// (internal/thump/reversal.go:55-58), so no undo order is ever published. A
// Record holding one Outcome cannot tell that apart from a converged run,
// which is why Settled is a slice.
func TestFoldRecord_AnUndoThatWasHeldIsDistinguishableFromAnUndoThatRan(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)
	t2 := t0.Add(10 * time.Minute)

	out1 := outcome.Outcome{
		SignalRef:   "fp-1",
		DecisionRef: "dec-1",
		ContractRef: "action-1",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultPartialNonConverging,
		Error:       "diverging past window",
		ExecutedAt:  t1,
	}
	outUndo := outcome.Outcome{
		SignalRef:   "fp-1",
		DecisionRef: "dec-1",
		ContractRef: "action-1-undo",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultSuccess,
		ExecutedAt:  t2,
	}

	tests := map[string]struct {
		outcomes []outcome.Outcome
		wantLen  int
		wantLast outcome.Result
	}{
		"an incident with a single outcome represents an action that ran without an undo": {
			outcomes: []outcome.Outcome{out1},
			wantLen:  1,
			wantLast: outcome.ResultPartialNonConverging,
		},
		"an incident with two outcomes represents an action whose undo executed and settled": {
			outcomes: []outcome.Outcome{out1, outUndo},
			wantLen:  2,
			wantLast: outcome.ResultSuccess,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var rec incident.Record
			for _, o := range tc.outcomes {
				rec = incident.FoldRecord(rec, o)
			}

			if diff := cmp.Diff(tc.wantLen, len(rec.Settled)); diff != "" {
				t.Errorf("wrong count of settled outcomes: %s", diff)
			}
			if len(rec.Settled) > 0 {
				if diff := cmp.Diff(tc.wantLast, rec.Settled[len(rec.Settled)-1].Result); diff != "" {
					t.Errorf("wrong last outcome result: %s", diff)
				}
			}
		})
	}
}

// TestFoldRecord_TheProjectionInventsNothingTheStreamDidNotCarry pins the
// package's own stated incapacity against the new fields: an object that
// arrives without a Set leaves Proposed nil rather than an empty Set, the
// same way Severity is a pointer so nil means unmeasured and never a
// fabricated 0.0 sitting next to a real one (incident.go:42-45).
func TestFoldRecord_TheProjectionInventsNothingTheStreamDidNotCarry(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	det := signal.Detection{
		Fingerprint:   "fp-1",
		OriginService: "cart",
		DetectedAt:    t0,
	}

	rec := incident.FoldRecord(incident.Record{}, det)

	if rec.Detected == nil {
		t.Fatal("want Detected populated, got nil")
	}
	if rec.Proposed != nil {
		t.Errorf("want Proposed nil when unobserved, got %+v", rec.Proposed)
	}
	if rec.Decided != nil {
		t.Errorf("want Decided nil when unobserved, got %+v", rec.Decided)
	}
	if len(rec.Settled) != 0 {
		t.Errorf("want Settled empty when unobserved, got %+v", rec.Settled)
	}
}

// TestRender_AnActionThatConvergedItsOwnMetricShowsTheFiredSLIBesideIt pins
// the acme tautology as legible rather than as a grader special case:
// acme-shed-load's criterion is sum(active)/sum(max) < 0.5 and the action
// raises sum(max) by scaling (config/dev/actions/catalog.yaml:283-291), so a
// success is guaranteed by acting. The artifact must print the SLO that fired
// and its post-action reading adjacent to the claimed result, so the
// contradiction is on the page instead of needing a test to catch it.
func TestRender_AnActionThatConvergedItsOwnMetricShowsTheFiredSLIBesideIt(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	sev1 := 1.0

	rec := incident.Record{
		Incident: incident.Incident{
			Fingerprint: "fp-acme-1",
			Service:     "acme-api",
			Stage:       incident.StageSettled,
			UpdatedAt:   t0.Add(5 * time.Minute),
			Severity:    &sev1,
		},
		Detected: &signal.Detection{
			Fingerprint:   "fp-acme-1",
			OriginService: "acme-api",
			DetectedAt:    t0,
			Divergence: signal.Divergence{
				Metric:     "acme_api_error_ratio",
				Observed:   1.0,
				Baseline:   0.0,
				Confidence: 1.0,
			},
		},
		Proposed: &proposal.Set{
			SignalRef:   "fp-acme-1",
			Recommended: "cand-1",
			Proposals: []proposal.Candidate{
				{
					ID:          "cand-1",
					ContractRef: "acme-shed-load",
					Rank:        1,
				},
			},
		},
		Decided: &decision.Decision{
			SignalRef:    "fp-acme-1",
			CandidateRef: "cand-1",
			Verdict:      decision.VerdictApproved,
		},
		Settled: []outcome.Outcome{
			{
				SignalRef:        "fp-acme-1",
				DecisionRef:      "dec-1",
				ContractRef:      "acme-shed-load",
				Mode:             outcome.ModeLive,
				Result:           outcome.ResultSuccess,
				ObservedSeverity: &sev1,
				ExecutedAt:       t0.Add(5 * time.Minute),
			},
		},
	}

	var buf bytes.Buffer
	if err := incident.Render(&buf, rec); err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	got := buf.String()

	// The artifact must show the fired SLO metric name, the claimed success result,
	// and the post-action observed severity reading (1.0).
	if !strings.Contains(got, "acme_api_error_ratio") {
		t.Errorf("want artifact to name the fired SLI metric 'acme_api_error_ratio', got:\n%s", got)
	}
	if !strings.Contains(got, "success") {
		t.Errorf("want artifact to show outcome result 'success', got:\n%s", got)
	}
	if !strings.Contains(got, "1.0") && !strings.Contains(got, "1.00") {
		t.Errorf("want artifact to show observed severity 1.0 adjacent to outcome, got:\n%s", got)
	}
}

// TestRender_TheArtifactCarriesNoRateNoAggregateAndNoOtherIncident pins this
// phase's subtraction. A percentage over four co-authored fixture rows at n=1
// is what five phases negotiated with; the artifact judges one incident on
// its own record, so a rate appearing anywhere in it would restore exactly
// the thing being removed.
func TestRender_TheArtifactCarriesNoRateNoAggregateAndNoOtherIncident(t *testing.T) {
	t.Parallel()

	rec := incident.Record{
		Incident: incident.Incident{
			Fingerprint: "fp-1",
			Service:     "checkout",
			Stage:       incident.StageSettled,
		},
		Detected: &signal.Detection{
			Fingerprint:   "fp-1",
			OriginService: "checkout",
		},
		Proposed: &proposal.Set{
			SignalRef:   "fp-1",
			Recommended: "cand-1",
			Proposals: []proposal.Candidate{
				{ID: "cand-1", ContractRef: "disable-cart-failure", Rank: 1},
			},
		},
		Decided: &decision.Decision{
			SignalRef:    "fp-1",
			CandidateRef: "cand-1",
			Verdict:      decision.VerdictApproved,
		},
		Settled: []outcome.Outcome{
			{
				SignalRef:   "fp-1",
				ContractRef: "disable-cart-failure",
				Result:      outcome.ResultSuccess,
			},
		},
	}

	var buf bytes.Buffer
	if err := incident.Render(&buf, rec); err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	got := buf.String()

	bannedPhrases := []string{
		"rate=",
		"rate:",
		"pass rate",
		"100.0%",
		"100%",
		"runs=",
		"hits=",
		"misses=",
		"harnessExcluded",
		"<html>",
		"<table",
	}

	for _, banned := range bannedPhrases {
		if strings.Contains(strings.ToLower(got), strings.ToLower(banned)) {
			t.Errorf("artifact carries banned aggregate / rate / markup phrase %q:\n%s", banned, got)
		}
	}
}

// TestRender_AllSixSectionsRenderInAuditOrder asserts that all six audit
// sections render in chronological order: detection -> evidence -> proposals ->
// governance -> order -> outcomes.
func TestRender_AllSixSectionsRenderInAuditOrder(t *testing.T) {
	t.Parallel()

	rec := incident.Record{
		Incident: incident.Incident{
			Fingerprint: "fp-1",
			Service:     "cart",
			Stage:       incident.StageSettled,
		},
		Detected: &signal.Detection{
			Fingerprint:   "fp-1",
			OriginService: "cart",
		},
		Proposed: &proposal.Set{
			SignalRef: "fp-1",
			Evidence: []proposal.EvidenceRef{
				{Tool: "kube", Key: "ev-1", Summary: "pod error", Live: true, Subject: "cart"},
			},
			Proposals: []proposal.Candidate{
				{ID: "cand-1", ContractRef: "disable-cart-failure", Rank: 1},
				{ID: "cand-2", ContractRef: "restart-cart", Rank: 2},
			},
			Recommended: "cand-1",
		},
		Decided: &decision.Decision{
			SignalRef:    "fp-1",
			CandidateRef: "cand-1",
			Verdict:      decision.VerdictApproved,
		},
		Settled: []outcome.Outcome{
			{SignalRef: "fp-1", ContractRef: "disable-cart-failure", Result: outcome.ResultSuccess},
		},
	}

	var buf bytes.Buffer
	if err := incident.Render(&buf, rec); err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	got := buf.String()

	sec1 := strings.Index(got, "1. WHAT FIRED")
	sec2 := strings.Index(got, "2. WHAT IT LOOKED AT")
	sec3 := strings.Index(got, "3. WHAT IT PROPOSED & DECLINED")
	sec4 := strings.Index(got, "4. WHAT GOVERNANCE RULED")
	sec5 := strings.Index(got, "5. WHAT RAN")
	sec6 := strings.Index(got, "6. WHAT HAPPENED")

	if sec1 == -1 || sec2 == -1 || sec3 == -1 || sec4 == -1 || sec5 == -1 || sec6 == -1 {
		t.Fatalf("missing one or more sections (sec1=%d sec2=%d sec3=%d sec4=%d sec5=%d sec6=%d):\n%s", sec1, sec2, sec3, sec4, sec5, sec6, got)
	}

	if sec1 >= sec2 || sec2 >= sec3 || sec3 >= sec4 || sec4 >= sec5 || sec5 >= sec6 {
		t.Errorf("sections are not in audit order (sec1=%d sec2=%d sec3=%d sec4=%d sec5=%d sec6=%d):\n%s", sec1, sec2, sec3, sec4, sec5, sec6, got)
	}
}

// TestRender_BankedAVCartRunRendersAllCausalScoresAndConfidenceTerms pins DoD item 2:
// loading bin/transcripts/slo_burn:cart/1786969860387330045/run.set.json and rendering it
// verifies that computedConfidence 1.00, emittedConfidence 0.85, the seven CausalScores
// (including out-of-topology acme events and checkout's 0.197 topological score) render accurately.
func TestRender_BankedAVCartRunRendersAllCausalScoresAndConfidenceTerms(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "bin", "transcripts", "slo_burn:cart", "1786969860387330045", "run.set.json")
	data, err := os.ReadFile(path) //nolint:gosec // G304: fixed testdata path, not user input
	if err != nil {
		t.Fatalf("failed to read cart transcript set: %v", err)
	}

	var set proposal.Set
	if err := json.Unmarshal(data, &set); err != nil {
		t.Fatalf("failed to unmarshal cart transcript set: %v", err)
	}

	rec := incident.Record{
		Incident: incident.Incident{
			Fingerprint: set.SignalRef,
			Service:     "cart",
			Stage:       incident.StageProposed,
		},
		Proposed: &set,
	}

	var buf bytes.Buffer
	if err := incident.Render(&buf, rec); err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	got := buf.String()

	// Assert computed confidence, emitted confidence, and causal scores
	for _, wantStr := range []string{
		"emitted=0.85",
		"computed=1.00",
		"deploy/otel-demo/checkout/1",
		"topological=0.1971",
		"deploy/acme/acme-api/1 (inTopology=false",
		"config/acme/acme-fault-flag/3397 (inTopology=false",
	} {
		if !strings.Contains(got, wantStr) {
			t.Errorf("rendered transcript missing expected substring %q:\n%s", wantStr, got)
		}
	}
}
