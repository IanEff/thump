package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const StreamName = "THUMP"

// maxDeliver is the retry budget: how many times the server will deliver a
// message (including the first try) before the subscriber gives up and
// dead-letters it. Single-sourced here; EnsureTopology configures the
// consumer with it, Run compares against it.
const maxDeliver = 6

var Subjects = []string{"thump.detections", "thump.proposals", "thump.decisions", "thump.outcomes"}

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
