package hiss

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// rebuildHolds replays hiss's own thump.decisions history and returns a
// PendingHolds seeded with every fingerprint whose latest decision was
// still VerdictHold, rebuilding what a restart would otherwise drop —
// bounded by the shared stream's 48h retention, not a permanent archive.
func rebuildHolds(ctx context.Context, js jetstream.JetStream) (*PendingHolds, error) {
	// No Durable name: this consumer's cursor only needs to survive one
	// startup pass, not a restart — an ordered (client-managed) consumer
	// tears itself down and re-delivers from the start on every
	// FetchNoWait call instead of advancing, so a plain acked consumer is
	// what actually makes forward progress across repeated Fetch calls.
	cons, err := js.CreateConsumer(ctx, broker.StreamName, jetstream.ConsumerConfig{
		FilterSubject:     "thump.decisions",
		AckPolicy:         jetstream.AckExplicitPolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("hiss: rebuild holds: create consumer: %w", err)
	}

	latest := make(map[string]decision.Governed)
	for {
		batch, err := cons.FetchNoWait(256)
		if err != nil {
			return nil, fmt.Errorf("hiss: rebuild holds: fetch: %w", err)
		}

		var n int
		for msg := range batch.Messages() {
			var g decision.Governed
			if err := wire.Unmarshal(msg.Data(), &g); err != nil {
				_ = msg.Ack() // poison would already be on the .dlq from the first pass
				continue
			}
			latest[g.Decision.SignalRef] = g
			_ = msg.Ack()
			n++
		}
		if err := batch.Error(); err != nil {
			return nil, fmt.Errorf("hiss: rebuild holds: batch: %w", err)
		}
		if n == 0 {
			break // caught up
		}
	}

	holds := NewPendingHolds()
	for _, g := range latest {
		if g.Decision.Verdict == decision.VerdictHold {
			holds.Record(g)
		}
	}

	return holds, nil
}
