package broker

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Connect dials NATS, opens a JetStream context, and ensures the shared
// topology exists — the boot preamble every beat's broker branch otherwise
// repeats verbatim (nats.Connect → jetstream.New → EnsureTopology). The
// returned close func closes the underlying connection and is safe to call
// more than once.
func Connect(ctx context.Context, natsURL string) (jetstream.JetStream, func(), error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, nil, fmt.Errorf("broker: connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("broker: jetstream: %w", err)
	}
	if err := EnsureTopology(ctx, js); err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("broker: ensure topology: %w", err)
	}
	return js, nc.Close, nil
}
