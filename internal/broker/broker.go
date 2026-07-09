// Package broker is the NATS JetStream plumbing a beat's Transport runs
// over: one shared stream, the fixed subject-to-consumer wiring, and
// (subscriber.go) a subscriber that sorts every delivery failure into
// exactly one of two doors — dead-letter now (the message will never
// decode) or retry with backoff up to the consumer's redelivery budget
// (the handler failed but might succeed next time). It moves bytes; it
// never inspects the JSON payload beyond decoding it (internal/wire) — a
// beat's own Handler gives the bytes meaning.
package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// StreamName is the one JetStream stream every subject lives on — beats
// share it rather than each owning its own, so EnsureTopology reconciles a
// single stream, not one per beat.
const StreamName = "THUMP"

// maxDeliver is the retry budget: how many times the server will deliver a
// message (including the first try) before the subscriber gives up and
// dead-letters it. Single-sourced here; EnsureTopology configures the
// consumer with it, Run compares against it.
const maxDeliver = 6

// Subjects is the fixed list of subjects EnsureTopology provisions a
// durable consumer for — the four boundary-object edges the beats hand
// off across (rattle→clank, clank→hiss, hiss→thump, thump→clank).
var Subjects = []string{"thump.detections", "thump.proposals", "thump.decisions", "thump.outcomes"}

// DurableFor names the durable consumer that owns subject, one name per
// beat so each beat's read position survives its own restarts without
// racing another beat's cursor. Returns "" for a subject with no
// registered reader.
func DurableFor(subject string) string {
	switch subject {
	case "thump.detections":
		return "clank" // clank reads detections
	case "thump.proposals":
		return "hiss" // hiss reads proposals
	case "thump.decisions":
		return "thump" // thump reads decisions
	case "thump.outcomes":
		return "click" // click (clank's return edge) reads outcomes
	}
	return ""
}

// EnsureTopology creates or updates the shared stream and one durable,
// explicit-ack consumer per Subjects entry — idempotent, safe to call on
// every beat startup. MaxAge caps the stream at 48 hours; past that a
// beat's own WAL (internal/publish), not JetStream retention, is the
// durability leg.
func EnsureTopology(ctx context.Context, js jetstream.JetStream) error {
	// one stream, 48hr age cap
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamName,
		Subjects:  []string{"thump.>"}, // four subjects and their .dlq friends
		Retention: jetstream.LimitsPolicy,
		MaxAge:    48 * time.Hour,
	}); err != nil {
		return fmt.Errorf("broker: ensure stream: %w", err)
	}

	for _, subj := range Subjects {
		if _, err := js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
			Durable:       DurableFor(subj),
			FilterSubject: subj,
			AckPolicy:     jetstream.AckExplicitPolicy,
			AckWait:       30 * time.Second, // should be greater than slowest handler, the model call
			MaxDeliver:    maxDeliver,
			// No BackOff here: it would silently override NakWithDelay's
			// requested delay after the first retry (nats-server's
			// checkPending recomputes the redelivery deadline from the
			// consumer's own BackOff schedule whenever one is configured).
			// The subscriber owns the backoff schedule instead (JetSubscriber.Backoff).
		}); err != nil {
			return fmt.Errorf("broker: ensure consumer %s: %w", subj, err)
		}
	}
	return nil
}
