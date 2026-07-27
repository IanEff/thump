package broker

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ianeff/thump/internal/tlsx"
)

// Connect dials NATS and opens a JetStream context — the boot preamble every
// beat's broker branch otherwise repeats verbatim (nats.Connect →
// jetstream.New). It never calls EnsureTopology: creating or updating the
// shared stream and its consumers needs $JS.API.> permissions, and granting
// that to every beat would let a compromised one rewrite another beat's
// consumer config regardless of its own publish grants — so only the
// topology Job's identity holds it (see ConnectAndEnsure). tlsCfg is this
// beat's own leaf/CA triple; the zero Config is legal and dials in the
// clear, which every hermetic test does today and which production can no
// longer reach — internal/config's NATS_URL only accepts a nats or tls
// scheme, and R6 makes tls:// the deployed value. The returned close func
// closes the underlying connection and is safe to call more than once.
func Connect(_ context.Context, natsURL string, tlsCfg tlsx.Config) (jetstream.JetStream, func(), error) {
	var opts []nats.Option
	if tlsCfg != (tlsx.Config{}) {
		tc, err := tlsx.Client(tlsCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("broker: tls config: %w", err)
		}
		opts = append(opts, nats.Secure(tc))
	}

	nc, err := nats.Connect(natsURL, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("broker: connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("broker: jetstream: %w", err)
	}
	return js, nc.Close, nil
}

// ConnectAndEnsure is Connect plus EnsureTopology — the topology Job's own
// entry point, never a beat's. Beats connect with a cert scoped to their own
// subject and a narrow $JS.API grant for their durable; only the Job's
// identity holds the $JS.API.> this call needs.
func ConnectAndEnsure(ctx context.Context, natsURL string, tlsCfg tlsx.Config) (jetstream.JetStream, func(), error) {
	js, closeNC, err := Connect(ctx, natsURL, tlsCfg)
	if err != nil {
		return nil, nil, err
	}
	if err := EnsureTopology(ctx, js); err != nil {
		closeNC()
		return nil, nil, fmt.Errorf("broker: ensure topology: %w", err)
	}
	return js, closeNC, nil
}
