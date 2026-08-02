package beat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/health"
	"github.com/ianeff/thump/internal/publish"
	"github.com/nats-io/nats.go/jetstream"
)

// RunConsumer subscribes one subject and runs h until ctx is cancelled. A beat
// with two inbound edges (clank consumes both detections and outcomes) runs two
// of these under an errgroup — the kit deliberately keeps that composition in
// the beat, rather than hiding it behind a knob.
func RunConsumer[In any](ctx context.Context, js jetstream.JetStream, subject string, h broker.Handler[In]) error {
	return broker.NewJetSubscriber[In](js).Run(ctx, subject, h)
}

// BrokerHooks drives health from the broker connection's own lifecycle, and
// calls onClosed when the connection is gone for good rather than merely
// dropped. Tying readiness to a dependency is normally a cascade risk — every
// replica leaves its Service at once — but a beat is a pull consumer nothing
// routes traffic to, so leaving endpoints costs nothing and stops a rolling
// deploy from marching through a broker outage.
func BrokerHooks(h *health.Health, beatName string, onClosed func()) broker.Hooks {
	return broker.Hooks{
		OnDisconnect: func(err error) {
			slog.Warn("broker disconnected, retrying", "beat", beatName, "err", err)
			h.NotReady("broker unreachable")
		},
		OnReconnect: func() {
			slog.Info("broker reconnected", "beat", beatName)
			h.SetReady(true)
		},
		OnClosed: func() {
			slog.Error("broker connection closed for good", "beat", beatName)
			h.NotReady("broker connection closed")
			if onClosed != nil {
				onClosed()
			}
		},
	}
}

// AwaitConsumers confirms a durable consumer bind exists for each subject, then flips ready to true.
func AwaitConsumers(ctx context.Context, js jetstream.JetStream, ready *health.Health, subjects ...string) error {
	for _, subject := range subjects {
		if _, err := js.Consumer(ctx, broker.StreamName, broker.DurableFor(subject)); err != nil {
			return fmt.Errorf("beat: bind consumer %s: %w", subject, err)
		}
	}
	ready.SetReady(true)
	return nil
}

// NewWALPublisher builds the WAL-journaled JetStream publisher every output
// edge uses (the fact is written to the local WAL before it travels), plus its
// close func. A beat with two output subjects (thump: orders + outcomes) calls
// this twice. An empty walDir is rejected here so the caller reports it once.
// Returns the concrete *publish.WALPublisher, not the Publisher[Out]
// interface, so a caller can reach .WAL directly to hand it to
// beat.RunShipper — it still satisfies Publisher[Out] everywhere that's
// what's wanted, so no other call site changes shape.
func NewWALPublisher[Out any](js jetstream.JetStream, walDir, beatName, subject string, wal WALConfig) (*publish.WALPublisher[Out], func(context.Context) error, error) {
	if walDir == "" {
		return nil, nil, errors.New("WAL_DIR is required")
	}
	w := &publish.WAL{Dir: walDir, Beat: beatName, Subject: subject, MaxBytes: wal.MaxBytes, MaxAge: wal.MaxAge, SyncInterval: wal.SyncInterval}
	pub := &publish.WALPublisher[Out]{WAL: w, Next: publish.NewJetPublisher[Out](js)}
	return pub, w.Close, nil
}

// ErrBrokerClosed cancels a beat's run context when its broker connection is
// gone for good — the cause that separates a lost broker from a clean SIGTERM,
// which are otherwise the same cancelled context.
var ErrBrokerClosed = errors.New("broker connection closed")

// ExitOnError maps a runner's terminating error to a process exit code,
// swallowing the expected ctx-cancelled shutdown so a clean SIGTERM returns 0.
// A run cancelled by ErrBrokerClosed is not clean — it exits non-zero so the
// orchestrator restarts a beat that can no longer reach its broker.
func ExitOnError(ctx context.Context, err error) int {
	if errors.Is(context.Cause(ctx), ErrBrokerClosed) {
		slog.Error("exiting: broker connection lost for good")
		return 1
	}
	if err != nil && ctx.Err() == nil {
		slog.Error("broker run failed", "err", err)
		return 1
	}
	return 0
}
