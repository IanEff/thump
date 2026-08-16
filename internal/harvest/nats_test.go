package harvest_test

import (
	"context"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/harvest"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/tlsx"
)

// TestNATSWatcher_ReceivesAPublishedOutcomeWithoutDisturbingClicksDurable is
// the property the whole design rests on: a harvest run watches
// thump.outcomes alongside clank's own return edge (the durable named
// "click"), and the two must never compete for the same delivery. If
// NATSWatcher bound click's durable instead of its own ordered consumer,
// this message would show as delivered (NumPending 0) the moment the
// watcher read it — silently stealing an outcome production's own learning
// loop needed.
func TestNATSWatcher_ReceivesAPublishedOutcomeWithoutDisturbingClicksDurable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := natstest.URL(t)
	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := harvest.NewNATSWatcher(js)
	ch, err := w.Outcomes(watchCtx)
	if err != nil {
		t.Fatal("Outcomes:", err)
	}

	pub := publish.NewJetPublisher[outcome.Outcome](js)
	want := outcome.Outcome{SignalRef: "fp-1", Result: outcome.ResultSuccess, ExecutedAt: time.Now()}
	if err := pub.Publish(ctx, "thump.outcomes", want); err != nil {
		t.Fatal("publish:", err)
	}

	select {
	case got := <-ch:
		if got.SignalRef != want.SignalRef {
			t.Errorf("want SignalRef %q, got %q", want.SignalRef, got.SignalRef)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NATSWatcher never delivered the published outcome")
	}

	cons, err := js.Consumer(ctx, broker.StreamName, broker.DurableFor("thump.outcomes"))
	if err != nil {
		t.Fatal("bind click's durable:", err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatal("consumer info:", err)
	}
	if info.NumPending != 1 {
		t.Errorf("want click's durable still holding 1 undelivered message, got %d pending — a harvest watch must never consume from production's own consumer", info.NumPending)
	}
}

// TestNATSSetWatcher_ReceivesAPublishedSetWithoutDisturbingHissDurable
// mirrors the outcome case for thump.proposals: hiss's durable is named
// "hiss", and a harvest scenario reading a Set to grade confidence must
// leave hiss's own backlog untouched.
func TestNATSSetWatcher_ReceivesAPublishedSetWithoutDisturbingHissDurable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := natstest.URL(t)
	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := harvest.NewNATSSetWatcher(js)
	ch, err := w.Sets(watchCtx)
	if err != nil {
		t.Fatal("Sets:", err)
	}

	pub := publish.NewJetPublisher[proposal.Set](js)
	want := proposal.Set{SignalRef: "fp-1", Recommended: "p1"}
	if err := pub.Publish(ctx, "thump.proposals", want); err != nil {
		t.Fatal("publish:", err)
	}

	select {
	case got := <-ch:
		if got.SignalRef != want.SignalRef {
			t.Errorf("want SignalRef %q, got %q", want.SignalRef, got.SignalRef)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NATSSetWatcher never delivered the published set")
	}

	cons, err := js.Consumer(ctx, broker.StreamName, broker.DurableFor("thump.proposals"))
	if err != nil {
		t.Fatal("bind hiss's durable:", err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatal("consumer info:", err)
	}
	if info.NumPending != 1 {
		t.Errorf("want hiss's durable still holding 1 undelivered message, got %d pending — a harvest watch must never consume from production's own consumer", info.NumPending)
	}
}

// TestNATSDetectionWatcher_ReceivesAPublishedDetectionWithoutDisturbingClanksDurable
// mirrors the outcome case for thump.detections — the leg Lane A0's refusal
// grace depends on. clank's durable is named "clank"; a harvest watch must
// leave it untouched the same way it leaves click's and hiss's alone.
func TestNATSDetectionWatcher_ReceivesAPublishedDetectionWithoutDisturbingClanksDurable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := natstest.URL(t)
	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := harvest.NewNATSDetectionWatcher(js)
	ch, err := w.Detections(watchCtx)
	if err != nil {
		t.Fatal("Detections:", err)
	}

	pub := publish.NewJetPublisher[signal.Detection](js)
	want := signal.Detection{Fingerprint: "slo_burn:product-catalog"}
	if err := pub.Publish(ctx, "thump.detections", want); err != nil {
		t.Fatal("publish:", err)
	}

	select {
	case got := <-ch:
		if got.Fingerprint != want.Fingerprint {
			t.Errorf("want Fingerprint %q, got %q", want.Fingerprint, got.Fingerprint)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NATSDetectionWatcher never delivered the published detection")
	}

	cons, err := js.Consumer(ctx, broker.StreamName, broker.DurableFor("thump.detections"))
	if err != nil {
		t.Fatal("bind clank's durable:", err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatal("consumer info:", err)
	}
	if info.NumPending != 1 {
		t.Errorf("want clank's durable still holding 1 undelivered message, got %d pending — a harvest watch must never consume from production's own consumer", info.NumPending)
	}
}

// TestNATSDeclineWatcher_ReceivesAPublishedDeclineWithoutDisturbingClanksDeclineDurable
// mirrors the same property for thump.declines.
func TestNATSDeclineWatcher_ReceivesAPublishedDeclineWithoutDisturbingClanksDeclineDurable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := natstest.URL(t)
	js, closeNC, err := broker.ConnectAndEnsure(ctx, url, tlsx.Config{}, broker.Hooks{})
	if err != nil {
		t.Fatal("connect and ensure:", err)
	}
	defer closeNC()

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := harvest.NewNATSDeclineWatcher(js)
	ch, err := w.Declines(watchCtx)
	if err != nil {
		t.Fatal("Declines:", err)
	}

	pub := publish.NewJetPublisher[decision.Decision](js)
	want := decision.Decision{ID: "dec:1", SignalRef: "slo_burn:cart", Verdict: decision.VerdictRejected}
	if err := pub.Publish(ctx, "thump.declines", want); err != nil {
		t.Fatal("publish:", err)
	}

	select {
	case got := <-ch:
		if got.SignalRef != want.SignalRef {
			t.Errorf("want SignalRef %q, got %q", want.SignalRef, got.SignalRef)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NATSDeclineWatcher never delivered the published decline")
	}

	cons, err := js.Consumer(ctx, broker.StreamName, broker.DurableFor("thump.declines"))
	if err != nil {
		t.Fatal("bind clank's decline durable:", err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatal("consumer info:", err)
	}
	if info.NumPending != 1 {
		t.Errorf("want clank's decline durable still holding 1 undelivered message, got %d pending — a harvest watch must never consume from production's own consumer", info.NumPending)
	}
}
