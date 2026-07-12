package broker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/trace"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/tracing"
)

// shrinkAckWait overrides the thump.detections consumer's AckWait — already
// created by EnsureTopology at production's 30s — down to a test-fast
// window, so these tests can prove real redelivery behavior without sitting
// through 30 real seconds. JetStream allows updating an existing durable
// consumer's config in place.
func shrinkAckWait(t *testing.T, ctx context.Context, js jetstream.JetStream, ackWait time.Duration) { //nolint:revive
	t.Helper()
	if _, err := js.CreateOrUpdateConsumer(ctx, broker.StreamName, jetstream.ConsumerConfig{
		Durable:       broker.DurableFor("thump.detections"),
		FilterSubject: "thump.detections",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       ackWait,
		MaxDeliver:    6,
	}); err != nil {
		t.Fatal("shrink AckWait:", err)
	}
}

func TestSubscriber_DeliversAndHandsToHandler(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "slo_burn:ceph-rgw"}); err != nil {
		t.Fatal(err)
	}

	got := make(chan signal.Detection, 1)
	runErr := make(chan error, 1)
	sub := broker.NewJetSubscriber[signal.Detection](js)

	go func() {
		runErr <- sub.Run(ctx, "thump.detections", func(_ context.Context, d signal.Detection, _ func()) error {
			got <- d
			return nil
		})
	}()

	select {
	case d := <-got:
		if diff := cmp.Diff("slo_burn:ceph-rgw", d.Fingerprint); diff != "" {
			t.Error("fingerprint didn't survive the broker (-want +got)", diff)
		}
	case err := <-runErr:
		t.Fatal("subscriber run exited early:", err)
	case <-ctx.Done():
		t.Fatal("handler never received the message")
	}
}

// TestSubscriber_HandlerContextCarriesThePublishedTrace is the end-to-end B1
// wire pin: whatever trace was active when a message was Published must be
// reconstructed onto the ctx a Subscriber hands its Handler — through a real
// embedded JetStream, not a fake. This is what lets clank's Engine.Propose
// (given that ctx) nest its own spans under rattle's incident trace without
// clank ever knowing rattle exists.
func TestSubscriber_HandlerContextCarriesThePublishedTrace(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	fp := "slo_burn:ceph-rgw"
	want := tracing.TraceIDFromFingerprint(fp)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    want,
		SpanID:     trace.SpanID{1},
		TraceFlags: trace.FlagsSampled,
	})
	pubCtx := trace.ContextWithSpanContext(ctx, sc)

	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(pubCtx, "thump.detections", signal.Detection{Fingerprint: fp}); err != nil {
		t.Fatal(err)
	}

	gotCtx := make(chan context.Context, 1)
	runErr := make(chan error, 1)
	sub := broker.NewJetSubscriber[signal.Detection](js)

	go func() {
		runErr <- sub.Run(ctx, "thump.detections", func(hctx context.Context, _ signal.Detection, _ func()) error {
			gotCtx <- hctx
			return nil
		})
	}()

	select {
	case hctx := <-gotCtx:
		got := trace.SpanContextFromContext(hctx).TraceID()
		if got != want {
			t.Errorf("handler ctx carries trace_id %s, want %s — Run must extract the published trace from the message headers into the handler's ctx", got, want)
		}
	case err := <-runErr:
		t.Fatal("subscriber run exited early:", err)
	case <-ctx.Done():
		t.Fatal("handler never received the trace")
	}
}

// TestSubscriber_SlowHandlerWithoutHeartbeatGetsRedelivered is the negative
// control for the heartbeat test below: it proves this rig's shrunk AckWait
// really does trigger a JetStream redelivery of a still-running handler when
// nothing resets the deadline — the exact failure mode that showed up live
// on rook-gce-k3s (a ~44s reason loop against a 30s AckWait). Without this
// control, a green heartbeat test could just mean the rig never exercises
// AckWait at all.
func TestSubscriber_SlowHandlerWithoutHeartbeatGetsRedelivered(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}
	const ackWait = 300 * time.Millisecond
	shrinkAckWait(t, ctx, js, ackWait)

	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "slo_burn:argocd"}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	sub := broker.NewJetSubscriber[signal.Detection](js)
	go func() {
		_ = sub.Run(ctx, "thump.detections", func(_ context.Context, _ signal.Detection, _ func()) error {
			if calls.Add(1) == 1 {
				time.Sleep(3 * ackWait) // never calls heartbeat — AckWait should fire mid-sleep
			}
			return nil
		})
	}()

	deadline := time.After(5 * ackWait)
	for {
		select {
		case <-deadline:
			t.Fatalf("want a redelivery (>=2 calls) within %s, got %d call(s) — the AckWait rig isn't exercising redelivery, so the heartbeat test below wouldn't prove anything", 5*ackWait, calls.Load())
		case <-time.After(10 * time.Millisecond):
			if calls.Load() >= 2 {
				return
			}
		}
	}
}

// TestSubscriber_HeartbeatPreventsRedeliveryOfASlowHandler is the fix this
// investigation produced: a handler slower than AckWait, but that calls the
// heartbeat func Run hands it more often than AckWait, must not be
// redelivered out from under itself. Mirrors clank's real reason loop, which
// heartbeats once per checkpointed turn via HeartbeatingStore — see
// internal/clank/heartbeat.go.
func TestSubscriber_HeartbeatPreventsRedeliveryOfASlowHandler(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}
	const ackWait = 300 * time.Millisecond
	shrinkAckWait(t, ctx, js, ackWait)

	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "slo_burn:argocd"}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	done := make(chan struct{})
	sub := broker.NewJetSubscriber[signal.Detection](js)
	go func() {
		_ = sub.Run(ctx, "thump.detections", func(_ context.Context, _ signal.Detection, heartbeat func()) error {
			calls.Add(1)
			hbCtx, stop := context.WithCancel(ctx)
			defer stop()
			go func() {
				ticker := time.NewTicker(ackWait / 3) // well inside the shrunk AckWait
				defer ticker.Stop()
				for {
					select {
					case <-hbCtx.Done():
						return
					case <-ticker.C:
						heartbeat()
					}
				}
			}()
			time.Sleep(3 * ackWait) // longer than AckWait, shorter than the heartbeat should tolerate
			close(done)
			return nil
		})
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("slow handler never completed")
	}

	// Give the server a moment to reflect the Ack, then confirm no
	// redelivery happened while the handler was still working.
	time.Sleep(2 * ackWait)
	cons, err := js.Consumer(ctx, broker.StreamName, broker.DurableFor("thump.detections"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("want the handler invoked exactly once, got %d — the heartbeat should have kept AckWait from firing a redelivery mid-handler", got)
	}
	if info.NumRedelivered != 0 {
		t.Errorf("want 0 redeliveries, got %d", info.NumRedelivered)
	}
}
