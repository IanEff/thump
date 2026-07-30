package hiss_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/hiss"
)

func TestTranslateDecision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		spec       hiss.ApprovalRequestSpec
		approvedBy string
		wantAck    approval.Approval
		wantOK     bool
		wantErr    bool
	}{
		"TranslateDecision reports no ack and ok false when Decision is unset": {
			spec:       hiss.ApprovalRequestSpec{SignalRef: "fp-1"},
			approvedBy: "alice",
			wantOK:     false,
		},
		"TranslateDecision returns an Approval addressed to the fingerprint when Decision is approve": {
			spec:       hiss.ApprovalRequestSpec{SignalRef: "fp-1", Decision: "approve"},
			approvedBy: "alice",
			wantAck:    approval.Approval{SignalRef: "fp-1", Approver: "alice", ApprovedAt: now},
			wantOK:     true,
		},
		"TranslateDecision rejects an unrecognized Decision value": {
			spec:       hiss.ApprovalRequestSpec{SignalRef: "fp-1", Decision: "maybe"},
			approvedBy: "alice",
			wantErr:    true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := hiss.TranslateDecisionForTest(tc.spec, tc.approvedBy, now)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error for an unrecognized Decision value, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.wantOK, ok); diff != "" {
				t.Error("wrong ok (-want +got)", diff)
			}
			if diff := cmp.Diff(tc.wantAck, got); diff != "" {
				t.Error("wrong translated Approval (-want +got)", diff)
			}
		})
	}
}

// heldGoverned runs governedSet() through a real Transport under a policy
// that holds it, then takes the resulting hold back out — the same shape
// approve_test.go uses, so forceDecision is tested against a hold hiss
// actually produced, not a hand-built fixture that could drift from what
// handle really emits.
func heldGoverned(t *testing.T) decision.Governed {
	t.Helper()
	holds := hiss.NewPendingHolds()
	tr := &hiss.Transport{Pub: &fakeDecisionPub{}, Policy: holdPolicy(), Log: hiss.NewDecisionLog(), Holds: holds, Now: frozenNow}
	if err := tr.HandleForTest(context.Background(), governedSet(), nil); err != nil {
		t.Fatal(err)
	}
	held, ok := holds.Take(governedSet().SignalRef)
	if !ok {
		t.Fatal("setup: want a hold recorded for governedSet")
	}
	return held
}

func TestForceDecision_MarksAHeldDecisionForcedAndBypassesTheRiskCeiling(t *testing.T) {
	t.Parallel()
	held := heldGoverned(t)

	got, err := hiss.ForceDecisionForTest(held, "alice", frozenNow())
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(decision.VerdictApproved, got.Decision.Verdict); diff != "" {
		t.Error("wrong verdict on a forced decision (-want +got)", diff)
	}
	if diff := cmp.Diff(held.Decision.RequestedBand, got.Decision.GrantedBand); diff != "" {
		t.Error("granted band should equal the originally requested band (-want +got)", diff)
	}
	if !got.Decision.Forced {
		t.Error("want Forced true on a forced decision")
	}
	if diff := cmp.Diff("alice", got.Decision.Operator); diff != "" {
		t.Error("forced decision must carry the operator (-want +got)", diff)
	}
	if got.Decision.Approver != "" {
		t.Errorf("a forced decision must not also carry an Approver — earned via ack or pushed through break-glass, never both, got %q", got.Decision.Approver)
	}
	if len(got.Decision.Reasons) != 0 {
		t.Errorf("a forced decision must carry zero reasons, got %v", got.Decision.Reasons)
	}
	if diff := cmp.Diff(held.Decision.PolicyVersion, got.Decision.PolicyVersion); diff != "" {
		t.Error("forcing must not alter which policy version the original hold was evaluated under (-want +got)", diff)
	}
	if diff := cmp.Diff(held.Set, got.Set); diff != "" {
		t.Error("forcing must not alter the judged Set (-want +got)", diff)
	}
	if err := got.Decision.Auditable(); err != nil {
		t.Error("forced decision must be Auditable:", err)
	}
}

func TestNewApprovalRequestSpec_PopulatesFromAHeldDecision(t *testing.T) {
	t.Parallel()
	held := heldGoverned(t)

	want := hiss.ApprovalRequestSpec{
		SignalRef: held.Decision.SignalRef,
		Action:    held.Set.ContractRefFor(held.Decision.CandidateRef),
		Band:      string(held.Decision.RequestedBand),
		Reasons:   held.Decision.Reasons,
	}
	got := hiss.NewApprovalRequestSpecForTest(held)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong ApprovalRequestSpec built from held Governed (-want +got)", diff)
	}
	if got.Decision != "" {
		t.Errorf("a freshly built spec must carry no Decision yet — that's the human's field to write, not the constructor's, got %q", got.Decision)
	}
}
