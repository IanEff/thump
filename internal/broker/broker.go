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
// durable consumer for — the five boundary-object edges the beats hand
// off across (rattle→clank, clank→hiss, hiss→thump, thump→clank, and
// thump→clank's second edge for governance non-approvals).
var Subjects = []string{"thump.detections", "thump.proposals", "thump.decisions", "thump.outcomes", "thump.declines", "thump.approvals"}

// DurableFor names the durable consumer that owns subject, one name per
// beat so each beat's read position survives its own restarts without
// racing another beat's cursor. Returns "" for a subject with no
// registered reader. Every entry must be unique within this switch, even
// when the same beat reads two subjects (clank reads both thump.detections
// and thump.declines) — a durable consumer name is a consumer's identity on
// the shared THUMP stream, so reusing one across two FilterSubjects would
// silently rebind the existing consumer instead of creating a second one.
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
	case "thump.declines":
		return "clank-declines" // clank's ledger-closing consumer — a non-approval never goes through Click
	case "thump.approvals":
		return "his-approvals" // hiss's second subscriber
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
			// AckWait no longer needs to outlast the slowest handler (clank's
			// reason loop can run well past 30s) — Handler's heartbeat param
			// (subscriber.go) resets this deadline on real progress instead,
			// so 30s is just "how long with zero progress before we assume
			// the consumer is dead," not a guessed worst-case latency.
			AckWait:    30 * time.Second,
			MaxDeliver: maxDeliver,
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
