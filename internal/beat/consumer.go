package beat

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ianeff/thump/internal/broker"
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

// NewWALPublisher builds the WAL-journaled JetStream publisher every output
// edge uses (the fact is written to the local WAL before it travels), plus its
// close func. A beat with two output subjects (thump: orders + outcomes) calls
// this twice. An empty walDir is rejected here so the caller reports it once.
func NewWALPublisher[Out any](js jetstream.JetStream, walDir, beatName, subject string) (publish.Publisher[Out], func(context.Context) error, error) {
	if walDir == "" {
		return nil, nil, errors.New("WAL_DIR is required")
	}
	w := &publish.WAL{Dir: walDir, Beat: beatName, Subject: subject}
	pub := &publish.WALPublisher[Out]{WAL: w, Next: publish.NewJetPublisher[Out](js)}
	return pub, w.Close, nil
}

// ExitOnError maps a runner's terminating error to a process exit code,
// swallowing the expected ctx-cancelled shutdown so a clean SIGTERM returns 0.
func ExitOnError(ctx context.Context, err error) int {
	if err != nil && ctx.Err() == nil {
		slog.Error("broker run failed", "err", err)
		return 1
	}
	return 0
}
