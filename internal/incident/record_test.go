package incident_test

import (
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
