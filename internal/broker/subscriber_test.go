package broker_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/tracing"
)

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
		runErr <- sub.Run(ctx, "thump.detections", func(_ context.Context, d signal.Detection) error {
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
		runErr <- sub.Run(ctx, "thump.detections", func(hctx context.Context, _ signal.Detection) error {
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
