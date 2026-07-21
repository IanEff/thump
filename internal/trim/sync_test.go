package trim_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/trim"
)

func publishTo[T any](t *testing.T, js jetstream.JetStream, subject string, obj T) {
	t.Helper()
	if err := publish.NewJetPublisher[T](js).Publish(context.Background(), subject, obj); err != nil {
		t.Fatalf("publish %s: %v", subject, err)
	}
}

func TestNATSSync_Run_MaterializesEveryBoundaryObjectTypeThenTransportSnapshotReadsItBack(t *testing.T) {
	t.Parallel()
	const fp = "fp-1"
	const svc = "checkout-api"
	t0 := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	js := natstest.New(t)
	ctx := context.Background()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	set := proposal.Set{SignalRef: fp, SAOSnapshot: &proposal.SAO{Version: 1, AssembledAt: t0.Add(time.Minute)}}
	publishTo(t, js, "thump.detections", signal.Detection{Fingerprint: fp, OriginService: svc, DetectedAt: t0})
	publishTo(t, js, "thump.proposals", set)
	publishTo(t, js, "thump.decisions", decision.Governed{
		Decision: decision.Decision{
			SignalRef:     fp,
			Verdict:       decision.VerdictApproved,
			PolicyVersion: "policy-v3",
			EvaluatedAt:   t0.Add(2 * time.Minute),
		},
		Set: set,
	})
	publishTo(t, js, "thump.outcomes", outcome.Outcome{
		SignalRef:   fp,
		DecisionRef: "dec-1",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultApplied,
		ExecutedAt:  t0.Add(3 * time.Minute),
	})

	inbox := t.TempDir()
	n, err := (&trim.NATSSync{JS: js, Inbox: inbox}).Run(ctx)
	if err != nil {
		t.Fatal("sync run must not error:", err)
	}
	if n != 4 {
		t.Errorf("want 4 objects synced, got %d", n)
	}

	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(ctx); err != nil {
		t.Fatal("Transport.Tick over the synced inbox must not error:", err)
	}
	got, ok := tr.Proj.Get(fp)
	if !ok {
		t.Fatal("want an incident for fp-1, got none")
	}
	want := trim.Incident{Fingerprint: fp, Stage: trim.StageApplied, Service: svc, UpdatedAt: t0.Add(3 * time.Minute)}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong incident after syncing from NATS then folding the materialized inbox (-want +got)", diff)
	}
}

func TestNATSSync_Run_PreservesEmissionOrderWhenTwoDecisionsShareAFingerprint(t *testing.T) {
	t.Parallel()
	const fp = "fp-1"

	js := natstest.New(t)
	ctx := context.Background()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	set := proposal.Set{SignalRef: fp}
	held := decision.Governed{
		Decision: decision.Decision{SignalRef: fp, Verdict: decision.VerdictHold, PolicyVersion: "policy-v3", EvaluatedAt: time.Now()},
		Set:      set,
	}
	approved := held
	approved.Decision.Verdict = decision.VerdictApproved
	approved.Decision.Approver = "ian"
	approved.Decision.EvaluatedAt = held.Decision.EvaluatedAt.Add(time.Minute)

	publishTo(t, js, "thump.decisions", held)
	publishTo(t, js, "thump.decisions", approved) // arrives second — must win

	inbox := t.TempDir()
	if _, err := (&trim.NATSSync{JS: js, Inbox: inbox}).Run(ctx); err != nil {
		t.Fatal("sync run must not error:", err)
	}

	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, ok := tr.Proj.Get(fp)
	if !ok {
		t.Fatal("want an incident for fp-1, got none")
	}
	if got.Stage != trim.StageApproved {
		t.Errorf("want Stage %q (the later decision), got %q — emission order was lost", trim.StageApproved, got.Stage)
	}
}

func TestNATSSync_Run_SkipsAMessageThatFailsToDecodeWithoutFailingThePass(t *testing.T) {
	t.Parallel()

	js := natstest.New(t)
	ctx := context.Background()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	if _, err := js.Publish(ctx, "thump.detections", []byte("{{{ not json")); err != nil {
		t.Fatal(err)
	}
	publishTo(t, js, "thump.detections", signal.Detection{Fingerprint: "fp-good", DetectedAt: time.Now()})

	inbox := t.TempDir()
	n, err := (&trim.NATSSync{JS: js, Inbox: inbox}).Run(ctx)
	if err != nil {
		t.Fatal("a poison message must be skipped, not fail the whole pass:", err)
	}
	if n != 1 {
		t.Errorf("want 1 object synced (the good one), got %d", n)
	}

	tr := &trim.Transport{Inbox: inbox, Proj: trim.NewProjection()}
	if err := tr.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.Proj.Get("fp-good"); !ok {
		t.Error("want the good detection to have survived the pass, got none")
	}
}

func TestNATSSync_Run_ReturnsZeroWhenTheStreamHasNoMessagesYet(t *testing.T) {
	t.Parallel()

	js := natstest.New(t)
	ctx := context.Background()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	inbox := t.TempDir()
	n, err := (&trim.NATSSync{JS: js, Inbox: inbox}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("want 0 objects synced from an empty stream, got %d", n)
	}
}

func TestNATSSync_Run_ReturnsContextErrorWithoutFetchingAnything(t *testing.T) {
	t.Parallel()

	js := natstest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (&trim.NATSSync{JS: js, Inbox: t.TempDir()}).Run(ctx); err == nil {
		t.Fatal("want an error from an already-cancelled context, got nil")
	}
}
