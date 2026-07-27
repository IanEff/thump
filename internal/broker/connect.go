package broker

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ianeff/thump/internal/tlsx"
)

const (
	// reconnectWait paces retries after a drop — matching nats.go's own
	// default, since there is nothing to gain by hammering a broker that is
	// restarting.
	reconnectWait = 2 * time.Second
	// reconnectJitter spreads the retries of beats that dropped together, so a
	// broker restart isn't met by all four reconnecting in lockstep.
	reconnectJitter = 500 * time.Millisecond
)

// Hooks observe a connection's lifecycle so a beat can report a broker it can
// no longer reach. The zero Hooks is legal and silent — the offline path has
// no readiness surface to drive — but a beat that passes it is a beat whose
// liveness says nothing about its ability to work.
type Hooks struct {
	OnDisconnect func(error) // the connection dropped; it is retrying
	OnReconnect  func()      // a retry succeeded and traffic can flow again
	OnClosed     func()      // terminal — this connection will never carry traffic again, and it was not our own shutdown
}

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
// scheme, and R6 makes tls:// the deployed value. An established connection
// retries forever rather than exhausting a budget and going quiet; the
// initial dial still fails fast, so a beat started before its broker exits
// and is restarted rather than idling. The returned close func closes the
// underlying connection, suppresses hooks.OnClosed for that shutdown, and is
// safe to call more than once.
func Connect(_ context.Context, natsURL string, tlsCfg tlsx.Config, hooks Hooks) (jetstream.JetStream, func(), error) {
	// closing separates our own shutdown from a connection nats.go has given
	// up on — it fires ClosedHandler for both, and only the second is a fault.
	var closing atomic.Bool
	opts, err := dialOptions(tlsCfg, hooks, &closing)
	if err != nil {
		return nil, nil, err
	}

	nc, err := nats.Connect(natsURL, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("broker: connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		closing.Store(true)
		nc.Close()
		return nil, nil, fmt.Errorf("broker: jetstream: %w", err)
	}
	return js, func() { closing.Store(true); nc.Close() }, nil
}

// dialOptions builds the nats options every beat connects under. nats.go's
// default gives up after 60 attempts (~2 minutes) and then sits Closed for the
// life of the process with nothing signalling it — a pod that stays Ready
// having permanently lost its only way to do work. A beat has nothing to do
// but retry, so it retries without a budget and reports its state through
// hooks instead.
func dialOptions(tlsCfg tlsx.Config, hooks Hooks, closing *atomic.Bool) ([]nats.Option, error) {
	opts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.ReconnectWait(reconnectWait),
		nats.ReconnectJitter(reconnectJitter, reconnectJitter),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if hooks.OnDisconnect != nil {
				hooks.OnDisconnect(err)
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) {
			if hooks.OnReconnect != nil {
				hooks.OnReconnect()
			}
		}),
		nats.ClosedHandler(func(*nats.Conn) {
			if closing.Load() || hooks.OnClosed == nil {
				return
			}
			hooks.OnClosed()
		}),
	}
	if tlsCfg != (tlsx.Config{}) {
		tc, err := tlsx.Client(tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("broker: tls config: %w", err)
		}
		opts = append(opts, nats.Secure(tc))
	}
	return opts, nil
}

// ConnectAndEnsure is Connect plus EnsureTopology — the topology Job's own
// entry point, never a beat's. Beats connect with a cert scoped to their own
// subject and a narrow $JS.API grant for their durable; only the Job's
// identity holds the $JS.API.> this call needs.
func ConnectAndEnsure(ctx context.Context, natsURL string, tlsCfg tlsx.Config, hooks Hooks) (jetstream.JetStream, func(), error) {
	js, closeNC, err := Connect(ctx, natsURL, tlsCfg, hooks)
	if err != nil {
		return nil, nil, err
	}
	if err := EnsureTopology(ctx, js); err != nil {
		closeNC()
		return nil, nil, fmt.Errorf("broker: ensure topology: %w", err)
	}
	return js, closeNC, nil
}
