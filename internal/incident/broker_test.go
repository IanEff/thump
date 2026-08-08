package incident_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/incident"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/nats-io/nats.go/jetstream"
)

// TestSnapshotBroker_FoldsHeldDecisionsTheDiskPathCannotSee pins the read
// path against the transport the cluster actually runs on. The beats write
// JetStream in broker mode and nothing lands on disk, so an operator asking
// for the queue got an empty list rather than an error — and a read path
// that silently finds nothing looks exactly like a quiet cluster.
func TestSnapshotBroker_FoldsHeldDecisionsTheDiskPathCannotSee(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	if err := publish.NewJetPublisher[signal.Detection](js).
		Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "slo_burn:cart"}); err != nil {
		t.Fatal(err)
	}
	if err := publish.NewJetPublisher[decision.Governed](js).
		Publish(ctx, "thump.decisions", heldGoverned("slo_burn:cart")); err != nil {
		t.Fatal(err)
	}

	proj, err := incident.SnapshotBroker(ctx, js)
	if err != nil {
		t.Fatal(err)
	}

	inc, ok := proj.Get("slo_burn:cart")
	if !ok {
		t.Fatal("want the held decision folded into the projection, got no incident at that fingerprint")
	}
	if inc.Held == nil {
		t.Fatal("want a held decision on the incident, got none")
	}
	if diff := cmp.Diff(decision.VerdictHold, inc.Held.Decision.Verdict); diff != "" {
		t.Error("wrong verdict folded from the broker", diff)
	}
}

// TestSnapshotBroker_OrdersEverySubjectsHistoryByPublishTime is the one that
// catches the subject-at-a-time bug. Fold is a state machine over the
// interleaving of four subjects, so draining them one subject at a time
// rewinds a settled incident to whatever its last-drained subject said.
func TestSnapshotBroker_OrdersEverySubjectsHistoryByPublishTime(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	// Published newest-subject-first on purpose: detections drains before
	// outcomes, so a naive implementation ends on the Detection and reports
	// StageDetected for an incident that has already settled.
	publishSettled(ctx, t, js, "slo_burn:cart")

	proj, err := incident.SnapshotBroker(ctx, js)
	if err != nil {
		t.Fatal(err)
	}

	inc, _ := proj.Get("slo_burn:cart")
	if diff := cmp.Diff(incident.StageSettled, inc.Stage); diff != "" {
		t.Error("wrong stage — subjects folded in drain order, not publish order", diff)
	}
}

// heldGoverned is one governed decision parked on a human, the shape
// SnapshotBroker exists to surface.
func heldGoverned(fingerprint string) decision.Governed {
	return decision.Governed{
		Decision: decision.Decision{
			ID: "dec:" + fingerprint, SignalRef: fingerprint,
			Verdict:       decision.VerdictHold,
			RequestedBand: decision.BandActDisruptive,
			RiskBand:      decision.BandActDisruptive,
			PolicyVersion: "policy-v3",
			EvaluatedAt:   time.Now(),
			Reasons:       []string{decision.ReasonRiskCeiling},
		},
		Set: proposal.Set{SignalRef: fingerprint},
	}
}

// publishSettled publishes one complete journey for fingerprint across all
// four subjects — detected, proposed, approved, then a successful outcome —
// so a Projection folded from it should land on StageSettled. Published in
// story order: DrainSubject's `at` is the JetStream ingestion timestamp, so
// publish order here is what SnapshotBroker's global sort must honor.
func publishSettled(ctx context.Context, t *testing.T, js jetstream.JetStream, fingerprint string) {
	t.Helper()

	if err := publish.NewJetPublisher[signal.Detection](js).
		Publish(ctx, "thump.detections", signal.Detection{Fingerprint: fingerprint}); err != nil {
		t.Fatal(err)
	}
	if err := publish.NewJetPublisher[proposal.Set](js).
		Publish(ctx, "thump.proposals", proposal.Set{SignalRef: fingerprint}); err != nil {
		t.Fatal(err)
	}
	if err := publish.NewJetPublisher[decision.Governed](js).
		Publish(ctx, "thump.decisions", decision.Governed{
			Decision: decision.Decision{
				ID: "dec:" + fingerprint, SignalRef: fingerprint,
				Verdict:     decision.VerdictApproved,
				EvaluatedAt: time.Now(),
			},
			Set: proposal.Set{SignalRef: fingerprint},
		}); err != nil {
		t.Fatal(err)
	}
	if err := publish.NewJetPublisher[outcome.Outcome](js).
		Publish(ctx, "thump.outcomes", outcome.Outcome{
			SignalRef:  fingerprint,
			Result:     outcome.ResultSuccess,
			ExecutedAt: time.Now(),
		}); err != nil {
		t.Fatal(err)
	}
}
