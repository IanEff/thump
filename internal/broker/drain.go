package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// DrainSubject reads subject's full history from the shared stream through a
// non-durable consumer — its read position exists only for this one pass,
// never persisted, so it never competes with a beat's own durable consumer
// on the same subject — and hands each decoded message to fold in the order
// the stream stored them. prefix names the caller in any error DrainSubject
// returns, so a rebuild failure still reads as "hiss: rebuild holds: ..." or
// "fetch thump.proposals: ..." rather than a generic message. A message that
// fails to decode, or whose metadata can't be read, is Acked and dropped
// rather than failing the drain: poison on the wire is a defect in whatever
// published it, not a reason to refuse every other message's history.
func DrainSubject[T any](ctx context.Context, js jetstream.JetStream, subject, prefix string, fold func(at time.Time, v T)) error {
	// No Durable name: this consumer's cursor only needs to survive one
	// startup pass, not a restart — an ordered (client-managed) consumer
	// tears itself down and re-delivers from the start on every
	// FetchNoWait call instead of advancing, so a plain acked consumer is
	// what actually makes forward progress across repeated Fetch calls.
	cons, err := js.CreateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		FilterSubject:     subject,
		AckPolicy:         jetstream.AckExplicitPolicy,
		InactiveThreshold: time.Minute,
	})
	if err != nil {
		return fmt.Errorf("%s: create consumer: %w", prefix, err)
	}

	for {
		batch, err := cons.FetchNoWait(256)
		if err != nil {
			return fmt.Errorf("%s: fetch: %w", prefix, err)
		}

		var n int
		for msg := range batch.Messages() {
			var v T
			if err := wire.Unmarshal(msg.Data(), &v); err != nil {
				_ = msg.Ack() // poison would already be on the .dlq from the first pass
				continue
			}
			meta, err := msg.Metadata()
			if err != nil {
				_ = msg.Ack()
				continue
			}
			fold(meta.Timestamp, v)
			_ = msg.Ack()
			n++
		}
		if err := batch.Error(); err != nil {
			return fmt.Errorf("%s: batch: %w", prefix, err)
		}
		if n == 0 {
			break // caught up
		}
	}
	return nil
}
