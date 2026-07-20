package trim_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/trim"
)

func ptr(f float64) *float64 { return &f }

// TestFold walks one fingerprint's golden path — detected, proposed,
// held-for-you, approved, declined, applied, settled — asserting the whole
// Incident each step, not just Stage: Fold has to thread Service and
// Fingerprint forward from prior (no later object carries them) while
// actively resetting Held once the fingerprint leaves the held stage, and
// carrying Forced forward from whichever decision.Governed set it rather
// than re-deriving it from a later object.
func TestFold(t *testing.T) {
	t.Parallel()

	const fp = "fp-1"
	const svc = "checkout-api"

	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) // rattle detects
	t1 := t0.Add(2 * time.Minute)                       // clank proposes
	t2 := t0.Add(5 * time.Minute)                       // hiss's first verdict
	t2Reissue := t2.Add(3 * time.Minute)                // hiss re-issues after a human ack
	t3 := t0.Add(8 * time.Minute)                       // thump applies
	t4 := t0.Add(20 * time.Minute)                      // the outcome settles

	proposed := trim.Incident{Fingerprint: fp, Stage: trim.StageProposed, Service: svc, UpdatedAt: t1}
	approved := trim.Incident{Fingerprint: fp, Stage: trim.StageApproved, Service: svc, UpdatedAt: t2}
	applied := trim.Incident{Fingerprint: fp, Stage: trim.StageApplied, Service: svc, UpdatedAt: t3}
	forcedApproved := trim.Incident{Fingerprint: fp, Stage: trim.StageApproved, Service: svc, UpdatedAt: t2, Forced: true, Operator: "alice"}

	set := proposal.Set{
		SignalRef:   fp,
		ServiceTier: "tier-1",
		SAOSnapshot: &proposal.SAO{Version: 1, AssembledAt: t1},
	}
	heldGoverned := decision.Governed{
		Decision: decision.Decision{
			ID:            "dec-1",
			ProposalRef:   "set-1",
			SignalRef:     fp,
			CandidateRef:  "cand-1",
			Verdict:       decision.VerdictHold,
			RiskBand:      decision.BandActDisruptive,
			PolicyVersion: "policy-v3",
			EvaluatedAt:   t2,
		},
		Set: set,
	}
	held := trim.Incident{Fingerprint: fp, Stage: trim.StageHeld, Service: svc, UpdatedAt: t2, Held: &heldGoverned}

	tests := map[string]struct {
		prior trim.Incident
		obj   any
		want  trim.Incident
	}{
		"Fold advances to detected when the object is a signal.Detection": {
			prior: trim.Incident{},
			obj: signal.Detection{
				Fingerprint:   fp,
				OriginService: svc,
				DetectedAt:    t0,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageDetected, Service: svc, UpdatedAt: t0},
		},
		"Fold preserves Service from the original Detection when the next object is a proposal.Set that carries none": {
			prior: trim.Incident{Fingerprint: fp, Stage: trim.StageDetected, Service: svc, UpdatedAt: t0},
			obj:   set,
			want:  proposed,
		},
		"Fold falls back to prior.UpdatedAt when a proposal.Set arrives with a nil SAOSnapshot": {
			prior: trim.Incident{Fingerprint: fp, Stage: trim.StageDetected, Service: svc, UpdatedAt: t0},
			obj:   proposal.Set{SignalRef: fp, SAOSnapshot: nil},
			want:  trim.Incident{Fingerprint: fp, Stage: trim.StageProposed, Service: svc, UpdatedAt: t0},
		},
		"Fold advances to held-for-you and retains the Governed when the verdict is hold": {
			prior: proposed,
			obj:   heldGoverned,
			want:  held,
		},
		"Fold clears Held once hiss re-issues the Governed as approved": {
			prior: held,
			obj: decision.Governed{
				Decision: decision.Decision{
					ID:            "dec-2",
					ProposalRef:   "set-1",
					SignalRef:     fp,
					CandidateRef:  "cand-1",
					Verdict:       decision.VerdictApproved,
					RequestedBand: decision.BandActDisruptive,
					GrantedBand:   decision.BandActDisruptive,
					PolicyVersion: "policy-v3",
					EvaluatedAt:   t2Reissue,
				},
				Set: set,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageApproved, Service: svc, UpdatedAt: t2Reissue, Held: nil},
		},
		"Fold marks Forced and records the Operator when the granting Decision was pushed through the break-glass path": {
			prior: held,
			obj: decision.Governed{
				Decision: decision.Decision{
					ID:            "dec-3",
					ProposalRef:   "set-1",
					SignalRef:     fp,
					CandidateRef:  "cand-1",
					Verdict:       decision.VerdictApproved,
					RequestedBand: decision.BandActDisruptive,
					GrantedBand:   decision.BandActDisruptive,
					PolicyVersion: "policy-v3",
					EvaluatedAt:   t2Reissue,
					Forced:        true,
					Operator:      "alice",
				},
				Set: set,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageApproved, Service: svc, UpdatedAt: t2Reissue, Held: nil, Forced: true, Operator: "alice"},
		},
		"Fold advances to declined when the verdict is escalate": {
			prior: proposed,
			obj: decision.Governed{
				Decision: decision.Decision{
					SignalRef:     fp,
					Verdict:       decision.VerdictEscalate,
					Reasons:       []string{decision.ReasonConfidenceFloor},
					PolicyVersion: "policy-v3",
					EvaluatedAt:   t2,
				},
				Set: set,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageDeclined, Service: svc, UpdatedAt: t2},
		},
		"Fold advances to declined when the verdict is rejected": {
			prior: proposed,
			obj: decision.Governed{
				Decision: decision.Decision{
					SignalRef:     fp,
					Verdict:       decision.VerdictRejected,
					Reasons:       []string{decision.ReasonUngatedInput},
					PolicyVersion: "policy-v3",
					EvaluatedAt:   t2,
				},
				Set: set,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageDeclined, Service: svc, UpdatedAt: t2},
		},
		"Fold advances to applied when the outcome result is applied": {
			prior: approved,
			obj: outcome.Outcome{
				SignalRef:   fp,
				DecisionRef: "dec-2",
				ContractRef: "restart-pod",
				Mode:        outcome.ModeLive,
				Result:      outcome.ResultApplied,
				ExecutedAt:  t3,
			},
			want: applied,
		},
		"Fold carries Forced forward through a later Outcome — a forced approval is never rendered as earned, even once settled": {
			prior: forcedApproved,
			obj: outcome.Outcome{
				SignalRef:   fp,
				DecisionRef: "dec-3",
				ContractRef: "restart-pod",
				Mode:        outcome.ModeLive,
				Result:      outcome.ResultApplied,
				ExecutedAt:  t3,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageApplied, Service: svc, UpdatedAt: t3, Forced: true, Operator: "alice"},
		},
		"Fold advances to settled when the outcome result is success": {
			prior: applied,
			obj: outcome.Outcome{
				SignalRef:        fp,
				DecisionRef:      "dec-2",
				Mode:             outcome.ModeLive,
				Result:           outcome.ResultSuccess,
				ExecutedAt:       t4,
				ObservedSeverity: ptr(0.12),
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageSettled, Service: svc, UpdatedAt: t4, Severity: ptr(0.12)},
		},
		"Fold advances to settled when the outcome result is partial_non_converging": {
			prior: applied,
			obj: outcome.Outcome{
				SignalRef:   fp,
				DecisionRef: "dec-2",
				Mode:        outcome.ModeLive,
				Result:      outcome.ResultPartialNonConverging,
				Error:       "still diverging past the success window",
				ExecutedAt:  t4,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageSettled, Service: svc, UpdatedAt: t4},
		},
		"Fold preserves a nil ObservedSeverity as unmeasured rather than a fabricated zero": {
			prior: applied,
			obj: outcome.Outcome{
				SignalRef:        fp,
				DecisionRef:      "dec-2",
				Mode:             outcome.ModeLive,
				Result:           outcome.ResultSuccess,
				ExecutedAt:       t4,
				ObservedSeverity: nil,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageSettled, Service: svc, UpdatedAt: t4, Severity: nil},
		},
		"Fold keeps a real zero ObservedSeverity distinct from an unmeasured nil": {
			prior: applied,
			obj: outcome.Outcome{
				SignalRef:        fp,
				DecisionRef:      "dec-2",
				Mode:             outcome.ModeLive,
				Result:           outcome.ResultSuccess,
				ExecutedAt:       t4,
				ObservedSeverity: ptr(0.0),
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageSettled, Service: svc, UpdatedAt: t4, Severity: ptr(0.0)},
		},
		"Fold ignores an unknown object and returns prior unchanged": {
			prior: proposed,
			obj:   "not a boundary object",
			want:  proposed,
		},
		"Fold records the Approver when hiss re-issues a Governed through the ack path": {
			prior: held,
			obj: decision.Governed{
				Decision: decision.Decision{
					SignalRef: fp, Verdict: decision.VerdictApproved,
					RequestedBand: decision.BandActDisruptive, GrantedBand: decision.BandActDisruptive,
					PolicyVersion: "policy-v3", EvaluatedAt: t2Reissue, Approver: "alice",
				},
				Set: set,
			},
			want: trim.Incident{Fingerprint: fp, Stage: trim.StageApproved, Service: svc, UpdatedAt: t2Reissue, Held: nil, Approver: "alice"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := trim.Fold(tc.prior, tc.obj)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong incident state after fold", diff)
			}
		})
	}
}
