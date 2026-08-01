package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ProvisionedAckWait reports the AckWait the broker actually configures for
// subject's durable consumer, read back off a freshly-provisioned server
// rather than restated as a constant — a beat's handler timeout is only
// coherent against the deadline the server enforces, not against the number
// somebody typed twice.
func ProvisionedAckWait(ctx context.Context, js jetstream.JetStream, subject string) (time.Duration, error) {
	if err := EnsureTopology(ctx, js); err != nil {
		return 0, fmt.Errorf("broker: provision for ack timing: %w", err)
	}
	cons, err := js.Consumer(ctx, StreamName, DurableFor(subject))
	if err != nil {
		return 0, fmt.Errorf("broker: get consumer %s: %w", subject, err)
	}
	info, err := cons.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("broker: consumer info %s: %w", subject, err)
	}
	return info.Config.AckWait, nil
}
