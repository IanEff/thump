package broker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
)

func TestSubscriber_ARepeatedlyFailingHandlerLandsInTheDLQRatherThanVanishing(t *testing.T) {
	t.Parallel()
	// A hold verdict is by construction the high-blast-tier case, and its whole
	// audit surface is a publish. thump.held published under a missing
	// permission for an unknown number of runs and nothing in the suite says
	// where those messages went. Both DLQ publishes discard their error
	// (subscriber.go:94,118), so "it was dead-lettered" is currently an
	// assumption, not an observation — this reads the .dlq subject back.
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}
	shrinkAckWait(t, ctx, js, 100*time.Millisecond)

	// A DLQ subject has no consumer of its own — it is captured by the
	// stream's thump.> filter and read back with an ephemeral, so the
	// assertion is "the bytes are on the stream", not "someone acked them".
	dlq, err := js.OrderedConsumer(ctx, broker.StreamName, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{"thump.detections.dlq"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := signal.Detection{Fingerprint: "dlq-probe"}
	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", want); err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int64
	sub := broker.NewJetSubscriber[signal.Detection](js)
	sub.Backoff = []time.Duration{time.Millisecond} // production's 1s/5s/15s would make this a 21s test
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	go func() {
		_ = sub.Run(runCtx, "thump.detections", func(context.Context, signal.Detection, func()) error {
			attempts.Add(1)
			return errors.New("permanently failing handler")
		})
	}()

	msg, err := dlq.Next() // blocks until the budget is spent and DOOR 2 publishes
	if err != nil {
		t.Fatal("nothing reached thump.detections.dlq — a message that exhausted its retry budget vanished:", err)
	}
	cancel()

	var got signal.Detection
	if err := wire.Unmarshal(msg.Data(), &got); err != nil {
		t.Fatal("dead-lettered payload no longer decodes:", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("dead-lettered message is not the one that failed (-want +got)\n", diff)
	}
	// maxDeliver is 6 and unexported; the claim is "it stopped retrying",
	// which is what the budget is for — not the exact number.
	if n := attempts.Load(); n < 2 {
		t.Errorf("handler ran %d times — a dead-letter after a single attempt means DOOR 2 never retried at all", n)
	}
}
