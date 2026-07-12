package broker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// Poison lands in the DLQ, and the NEXT good message still gets processed.
func TestDoors_PoisonGoesToDLQ_AndDoesNotBlock(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	_, _ = js.Publish(ctx, "thump.detections", []byte("{{{ not json")) // poison
	pub := publish.NewJetPublisher[signal.Detection](js)
	_ = pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "good"}) // good, behind it

	good := make(chan signal.Detection, 1)
	sub := broker.NewJetSubscriber[signal.Detection](js)
	go func() {
		_ = sub.Run(ctx, "thump.detections", func(_ context.Context, d signal.Detection, _ func()) error {
			good <- d
			return nil
		})
	}()

	// the good message MUST arrive despite the poison ahead of it
	select {
	case d := <-good:
		if d.Fingerprint != "good" {
			t.Fatalf("wrong message got through: %+v", d)
		}
	case <-ctx.Done():
		t.Fatal("poison blocked the queue — head-of-line block, the bug we're preventing")
	}

	// and the poison is parked in the DLQ subject
	stream, _ := js.Stream(ctx, broker.StreamName)
	if _, err := stream.GetLastMsgForSubject(ctx, "thump.detections.dlq"); err != nil {
		t.Error("poison was not dead-lettered:", err)
	}
}

// fastBackoff stands in for JetSubscriber's production {1s, 5s, 15s} default
// so these tests don't sit around waiting on it.
var fastBackoff = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}

// A handler that fails K times then would-succeed dead-letters ONLY after the budget.
// A handler that fails once then succeeds must NOT dead-letter.
func TestDoors_TransientRetriesThenDLQ(t *testing.T) {
	t.Run("fails once then succeeds: acked, never dead-lettered", func(t *testing.T) {
		t.Parallel()
		js := natstest.New(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := broker.EnsureTopology(ctx, js); err != nil {
			t.Fatal(err)
		}

		pub := publish.NewJetPublisher[signal.Detection](js)
		if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "flaky"}); err != nil {
			t.Fatal(err)
		}

		var attempts atomic.Int32
		acked := make(chan signal.Detection, 1)
		sub := broker.NewJetSubscriber[signal.Detection](js)
		sub.Backoff = fastBackoff
		go func() {
			_ = sub.Run(ctx, "thump.detections", func(_ context.Context, d signal.Detection, _ func()) error {
				if attempts.Add(1) == 1 {
					return errors.New("simulated transient failure")
				}
				acked <- d
				return nil
			})
		}()

		select {
		case d := <-acked:
			if d.Fingerprint != "flaky" {
				t.Fatalf("wrong message acked: %+v", d)
			}
		case <-ctx.Done():
			t.Fatal("message never succeeded after retry")
		}

		// give the ack a moment to land, then confirm no DLQ entry showed up
		time.Sleep(50 * time.Millisecond)
		stream, err := js.Stream(ctx, broker.StreamName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.GetLastMsgForSubject(ctx, "thump.detections.dlq"); err == nil {
			t.Error("a message that eventually succeeded should never reach the DLQ")
		}
	})

	t.Run("fails every time: dead-letters only once the budget is spent", func(t *testing.T) {
		t.Parallel()
		js := natstest.New(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := broker.EnsureTopology(ctx, js); err != nil {
			t.Fatal(err)
		}

		pub := publish.NewJetPublisher[signal.Detection](js)
		if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "always-fails"}); err != nil {
			t.Fatal(err)
		}

		var deliveries atomic.Int32
		sub := broker.NewJetSubscriber[signal.Detection](js)
		sub.Backoff = fastBackoff
		go func() {
			_ = sub.Run(ctx, "thump.detections", func(_ context.Context, _ signal.Detection, _ func()) error {
				deliveries.Add(1)
				return errors.New("simulated permanent failure")
			})
		}()

		stream, err := js.Stream(ctx, broker.StreamName)
		if err != nil {
			t.Fatal(err)
		}

		var dlqMsg *jetstream.RawStreamMsg
		for dlqMsg == nil {
			select {
			case <-ctx.Done():
				t.Fatalf("never dead-lettered after %d deliveries", deliveries.Load())
			default:
			}
			if msg, err := stream.GetLastMsgForSubject(ctx, "thump.detections.dlq"); err == nil {
				dlqMsg = msg
				break
			}
			time.Sleep(20 * time.Millisecond)
		}

		var got signal.Detection
		if err := wire.Unmarshal(dlqMsg.Data, &got); err != nil {
			t.Fatal("dlq bytes didn't decode:", err)
		}
		if got.Fingerprint != "always-fails" {
			t.Fatalf("wrong message dead-lettered: %+v", got)
		}

		// the budget is 6 (broker.EnsureTopology's MaxDeliver) — the handler
		// must have actually been retried that many times, not dead-lettered
		// on the first failure.
		if n := deliveries.Load(); n != 6 {
			t.Errorf("expected exactly 6 deliveries before dead-lettering (the retry budget), handler saw %d", n)
		}
	})
}
