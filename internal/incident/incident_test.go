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

// TestFold walks one fingerprint's golden path — detected, proposed, held,
// decided, applied, settled — asserting the whole Incident each step, not
// just Stage: Fold has to thread Service and Fingerprint forward from prior
// (no later object carries them), and it replaces Governed outright on every
// new decision.Governed regardless of verdict — there is no branch that
// clears or resets it, so a later Outcome fold inherits whatever Governed
// the last decision left behind via the leading next := prior copy.
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
	reissuedApproved := decision.Governed{
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
	}
	forcedGoverned := decision.Governed{
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
	}
	escalatedGoverned := decision.Governed{
		Decision: decision.Decision{
			SignalRef:     fp,
			Verdict:       decision.VerdictEscalate,
			Reasons:       []string{decision.ReasonConfidenceFloor},
			PolicyVersion: "policy-v3",
			EvaluatedAt:   t2,
		},
		Set: set,
	}
	rejectedGoverned := decision.Governed{
		Decision: decision.Decision{
			SignalRef:     fp,
			Verdict:       decision.VerdictRejected,
			Reasons:       []string{decision.ReasonUngatedInput},
			PolicyVersion: "policy-v3",
			EvaluatedAt:   t2,
		},
		Set: set,
	}
	ackApproved := decision.Governed{
		Decision: decision.Decision{
			SignalRef:     fp,
			Verdict:       decision.VerdictApproved,
			RequestedBand: decision.BandActDisruptive,
			GrantedBand:   decision.BandActDisruptive,
			PolicyVersion: "policy-v3",
			EvaluatedAt:   t2Reissue,
			Approver:      "alice",
		},
		Set: set,
	}

	proposed := incident.Incident{Fingerprint: fp, Stage: incident.StageProposed, Service: svc, UpdatedAt: t1}
	held := incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2, Governed: &heldGoverned}
	approved := incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2, Governed: &reissuedApproved}
	applied := incident.Incident{Fingerprint: fp, Stage: incident.StageApplied, Service: svc, UpdatedAt: t3, Governed: &reissuedApproved}
	forcedApproved := incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2, Governed: &forcedGoverned}

	tests := map[string]struct {
		prior incident.Incident
		obj   any
		want  incident.Incident
	}{
		"Fold advances to detected when the object is a signal.Detection": {
			prior: incident.Incident{},
			obj: signal.Detection{
				Fingerprint:   fp,
				OriginService: svc,
				DetectedAt:    t0,
			},
			want: incident.Incident{Fingerprint: fp, Stage: incident.StageDetected, Service: svc, UpdatedAt: t0},
		},
		"Fold preserves Service from the original Detection when the next object is a proposal.Set that carries none": {
			prior: incident.Incident{Fingerprint: fp, Stage: incident.StageDetected, Service: svc, UpdatedAt: t0},
			obj:   set,
			want:  proposed,
		},
		"Fold falls back to prior.UpdatedAt when a proposal.Set arrives with a nil SAOSnapshot": {
			prior: incident.Incident{Fingerprint: fp, Stage: incident.StageDetected, Service: svc, UpdatedAt: t0},
			obj:   proposal.Set{SignalRef: fp, SAOSnapshot: nil},
			want:  incident.Incident{Fingerprint: fp, Stage: incident.StageProposed, Service: svc, UpdatedAt: t0},
		},
		"Fold advances to held-for-you and retains the Governed when the verdict is hold": {
			prior: proposed,
			obj:   heldGoverned,
			want:  held,
		},
		"Fold replaces the prior Governed once hiss re-issues an approved verdict": {
			prior: held,
			obj:   reissuedApproved,
			want:  incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2Reissue, Governed: &reissuedApproved},
		},
		"Fold marks Forced and records the Operator when the granting Decision was pushed through the break-glass path": {
			prior: held,
			obj:   forcedGoverned,
			want:  incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2Reissue, Governed: &forcedGoverned},
		},
		"Fold advances to decided and retains the Governed when the verdict is escalate": {
			prior: proposed,
			obj:   escalatedGoverned,
			want:  incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2, Governed: &escalatedGoverned},
		},
		"Fold advances to decided and retains the Governed when the verdict is rejected": {
			prior: proposed,
			obj:   rejectedGoverned,
			want:  incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2, Governed: &rejectedGoverned},
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
			want: incident.Incident{Fingerprint: fp, Stage: incident.StageApplied, Service: svc, UpdatedAt: t3, Governed: &forcedGoverned},
		},
		"Fold advances to settled when the outcome result is success": {
			prior: applied,
			obj: outcome.Outcome{
				SignalRef:        fp,
				DecisionRef:      "dec-2",
				Mode:             outcome.ModeLive,
				Result:           outcome.ResultSuccess,
				ExecutedAt:       t4,
				ObservedSeverity: new(0.12),
			},
			want: incident.Incident{Fingerprint: fp, Stage: incident.StageSettled, Service: svc, UpdatedAt: t4, Governed: &reissuedApproved, Severity: new(0.12)},
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
			want: incident.Incident{Fingerprint: fp, Stage: incident.StageSettled, Service: svc, UpdatedAt: t4, Governed: &reissuedApproved},
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
			want: incident.Incident{Fingerprint: fp, Stage: incident.StageSettled, Service: svc, UpdatedAt: t4, Governed: &reissuedApproved, Severity: nil},
		},
		"Fold keeps a real zero ObservedSeverity distinct from an unmeasured nil": {
			prior: applied,
			obj: outcome.Outcome{
				SignalRef:        fp,
				DecisionRef:      "dec-2",
				Mode:             outcome.ModeLive,
				Result:           outcome.ResultSuccess,
				ExecutedAt:       t4,
				ObservedSeverity: new(0.0),
			},
			want: incident.Incident{Fingerprint: fp, Stage: incident.StageSettled, Service: svc, UpdatedAt: t4, Governed: &reissuedApproved, Severity: new(0.0)},
		},
		"Fold ignores an unknown object and returns prior unchanged": {
			prior: proposed,
			obj:   "not a boundary object",
			want:  proposed,
		},
		"Fold records the Approver when hiss re-issues a Governed through the ack path": {
			prior: held,
			obj:   ackApproved,
			want:  incident.Incident{Fingerprint: fp, Stage: incident.StageDecided, Service: svc, UpdatedAt: t2Reissue, Governed: &ackApproved},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := incident.Fold(tc.prior, tc.obj)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong incident state after fold", diff)
			}
		})
	}
}

// TestFold_KeepsEveryVerdictThatAwaitsAHumanForcible pins the read model to
// the same predicate hiss and thump read. An escalation folded as terminal
// left a fingerprint that hiss was holding open, and that thump had already
// paged a human about, rendering as declined and refusing a break-glass.
func TestFold_KeepsEveryVerdictThatAwaitsAHumanForcible(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verdict      decision.Verdict
		wantForcible bool
	}{
		"Fold leaves a held decision forcible through break-glass":       {decision.VerdictHold, true},
		"Fold leaves an escalated decision forcible through break-glass": {decision.VerdictEscalate, true},
		"Fold leaves an approved decision not forcible":                  {decision.VerdictApproved, false},
		"Fold leaves a rejected decision not forcible":                   {decision.VerdictRejected, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := incident.Fold(incident.Incident{}, governedWith(tc.verdict))

			if got.Governed == nil {
				t.Fatal("want the governed decision retained on the incident, got none")
			}
			if diff := cmp.Diff(tc.wantForcible, got.Governed.Decision.Verdict.AwaitsApproval()); diff != "" {
				t.Error("wrong forcible-through-break-glass verdict", diff)
			}
		})
	}
}

// governedWith returns a decision.Governed carrying only the given verdict —
// Fold reads nothing else about the Decision, so nothing else is needed here.
func governedWith(v decision.Verdict) decision.Governed {
	return decision.Governed{Decision: decision.Decision{Verdict: v}}
}
