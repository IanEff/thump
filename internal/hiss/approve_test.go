package hiss_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/hiss"
)

func TestApproveHandler_ReIssuesAnApprovedGovernedForAHeldFingerprint(t *testing.T) {
	t.Parallel()
	fake := &fakeDecisionPub{}
	holds := hiss.NewPendingHolds()
	tr := &hiss.Transport{Pub: fake, Policy: holdPolicy(), Log: hiss.NewDecisionLog(), Holds: holds, Now: frozenNow}
	if err := tr.HandleForTest(context.Background(), governedSet(), nil); err != nil {
		t.Fatal(err)
	}
	fake.published = nil // handle already published the hold itself; only the ack's re-issue is under test here

	ack := approval.Approval{SignalRef: governedSet().SignalRef, Approver: "alice", ApprovedAt: frozenNow()}
	if err := tr.ApproveHandlerForTest(context.Background(), ack, nil); err != nil {
		t.Fatal(err)
	}

	if len(fake.published) != 1 {
		t.Fatalf("want exactly one re-issued decision, got %d", len(fake.published))
	}
	got := fake.published[0].Decision
	if diff := cmp.Diff(decision.VerdictApproved, got.Verdict); diff != "" {
		t.Error("wrong verdict on the re-issued decision (-want +got)", diff)
	}
	if diff := cmp.Diff("alice", got.Approver); diff != "" {
		t.Error("re-issued decision must carry the approver (-want +got)", diff)
	}
	if diff := cmp.Diff(decision.BandObserve, got.GrantedBand); diff != "" {
		t.Error("granted band should equal the original requested band (-want +got)", diff)
	}
	if len(got.Reasons) != 0 {
		t.Errorf("an approved decision must carry zero reasons, got %v", got.Reasons)
	}
	if err := got.Auditable(); err != nil {
		t.Error("re-issued decision must be Auditable:", err)
	}
}

func TestApproveHandler_ASecondAckForTheSameFingerprintIsInert(t *testing.T) {
	t.Parallel()
	fake := &fakeDecisionPub{}
	holds := hiss.NewPendingHolds()
	tr := &hiss.Transport{Pub: fake, Policy: holdPolicy(), Log: hiss.NewDecisionLog(), Holds: holds, Now: frozenNow}
	if err := tr.HandleForTest(context.Background(), governedSet(), nil); err != nil {
		t.Fatal(err)
	}
	fake.published = nil // handle already published the hold itself; only the two acks below are under test here
	ack := approval.Approval{SignalRef: governedSet().SignalRef, Approver: "alice", ApprovedAt: frozenNow()}
	if err := tr.ApproveHandlerForTest(context.Background(), ack, nil); err != nil {
		t.Fatal(err)
	}

	// redelivery, or a second human clicking the same link
	if err := tr.ApproveHandlerForTest(context.Background(), ack, nil); err != nil {
		t.Fatal("a second ack must Ack (return nil), not error:", err)
	}

	if len(fake.published) != 1 {
		t.Errorf("want exactly one publish across both acks (I-14: approving twice executes once), got %d", len(fake.published))
	}
}

func TestApproveHandler_AckForAnUnheldFingerprintIsInert(t *testing.T) {
	t.Parallel()
	fake := &fakeDecisionPub{}
	tr := &hiss.Transport{Pub: fake, Policy: calmPolicy(), Log: hiss.NewDecisionLog(), Holds: hiss.NewPendingHolds(), Now: frozenNow}

	ack := approval.Approval{SignalRef: "no-such-fp", Approver: "alice", ApprovedAt: frozenNow()}
	if err := tr.ApproveHandlerForTest(context.Background(), ack, nil); err != nil {
		t.Fatal("an ack for an unknown fingerprint must Ack, not error:", err)
	}
	if len(fake.published) != 0 {
		t.Errorf("want nothing published for an unheld fingerprint, got %d", len(fake.published))
	}
}
