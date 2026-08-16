package harvest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/wire"
)

// NATSWatcher satisfies Watcher against a live cluster. It reads
// thump.outcomes through its own ordered, ephemeral consumer rather than
// broker.DurableFor's "click" — that name is clank's own return-edge
// consumer, and a second reader bound to it would split deliveries between
// the two: a harvest run competing with production learning for the same
// messages, each silently missing whatever the other one claimed.
type NATSWatcher struct{ js jetstream.JetStream }

// NewNATSWatcher builds a Watcher over an already-connected js.
func NewNATSWatcher(js jetstream.JetStream) *NATSWatcher { return &NATSWatcher{js: js} }

// Outcomes starts from "now": a harvest scenario watches for the outcome
// its own fault produces, never a backlog from before it started.
func (w *NATSWatcher) Outcomes(ctx context.Context) (<-chan outcome.Outcome, error) {
	return watchSubject[outcome.Outcome](ctx, w.js, "thump.outcomes", jetstream.DeliverNewPolicy)
}

// NATSSetWatcher is Watcher's mirror for thump.proposals, staying off
// hiss's durable ("hiss") for the same reason NATSWatcher stays off click's.
type NATSSetWatcher struct{ js jetstream.JetStream }

// NewNATSSetWatcher builds a SetWatcher over an already-connected js.
func NewNATSSetWatcher(js jetstream.JetStream) *NATSSetWatcher { return &NATSSetWatcher{js: js} }

// Sets starts from the beginning of the stream's retention, unlike
// Outcomes: by the time Settle subscribes, the Set it's looking for may
// already have been published — thump.proposals carries every fingerprint
// on one subject, so there is no per-key "last message" to jump to
// (DeliverLastPolicy would return the most recent Set for *any*
// fingerprint, not signalRef's), and DeliverNewPolicy would silently miss
// it every time. Settle's own SignalRef filter keeps a full replay correct
// even when it isn't cheap.
func (w *NATSSetWatcher) Sets(ctx context.Context) (<-chan proposal.Set, error) {
	return watchSubject[proposal.Set](ctx, w.js, "thump.proposals", jetstream.DeliverAllPolicy)
}

// NATSDeclineWatcher satisfies DeclineWatcher against a live cluster,
// staying off any durable consumer for the same reason NATSWatcher does.
type NATSDeclineWatcher struct{ js jetstream.JetStream }

// NewNATSDeclineWatcher builds a DeclineWatcher over an already-connected js.
func NewNATSDeclineWatcher(js jetstream.JetStream) *NATSDeclineWatcher {
	return &NATSDeclineWatcher{js: js}
}

// Declines starts from "now", matching Outcomes: a harvest scenario watches
// for the decline its own fault produces, never a backlog.
func (w *NATSDeclineWatcher) Declines(ctx context.Context) (<-chan decision.Decision, error) {
	return watchSubject[decision.Decision](ctx, w.js, "thump.declines", jetstream.DeliverNewPolicy)
}

// NATSHeldWatcher satisfies HeldWatcher against a live cluster.
type NATSHeldWatcher struct{ js jetstream.JetStream }

// NewNATSHeldWatcher builds a HeldWatcher over an already-connected js.
func NewNATSHeldWatcher(js jetstream.JetStream) *NATSHeldWatcher {
	return &NATSHeldWatcher{js: js}
}

// Held starts from "now", matching Outcomes.
func (w *NATSHeldWatcher) Held(ctx context.Context) (<-chan decision.Governed, error) {
	return watchSubject[decision.Governed](ctx, w.js, "thump.held", jetstream.DeliverNewPolicy)
}

// NATSDetectionWatcher satisfies DetectionWatcher against a live cluster.
type NATSDetectionWatcher struct{ js jetstream.JetStream }

// NewNATSDetectionWatcher builds a DetectionWatcher over an already-connected js.
func NewNATSDetectionWatcher(js jetstream.JetStream) *NATSDetectionWatcher {
	return &NATSDetectionWatcher{js: js}
}

// Detections starts from "now", matching Outcomes.
func (w *NATSDetectionWatcher) Detections(ctx context.Context) (<-chan signal.Detection, error) {
	return watchSubject[signal.Detection](ctx, w.js, "thump.detections", jetstream.DeliverNewPolicy)
}

// watchSubject opens an ordered consumer scoped to ctx and streams every
// decoded message of type T onto the returned channel. Ordered consumers
// are per-caller and ephemeral — the deliberate alternative to
// broker.JetSubscriber's durable, ack-tracked consumers, which a read-only
// observer like this must never bind (see NATSWatcher).
//
// The returned channel is deliberately never closed. Every reader (Settle)
// already selects on ctx.Done() alongside the channel receive, and closing
// here would race the callback goroutine below: a send still
// mid-select when ctx fires could be handed a channel that closed out from
// under it and panic. Leaving it open costs nothing — ctx bounds the
// producer goroutine's life either way, and an unread channel with no
// further senders is ordinary garbage once the caller drops its reference.
func watchSubject[T any](ctx context.Context, js jetstream.JetStream, subject string, policy jetstream.DeliverPolicy) (<-chan T, error) {
	cons, err := js.OrderedConsumer(ctx, broker.StreamName, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{subject},
		DeliverPolicy:  policy,
	})
	if err != nil {
		return nil, fmt.Errorf("harvest: ordered consumer %s: %w", subject, err)
	}

	ch := make(chan T)
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		var obj T
		if err := wire.Unmarshal(msg.Data(), &obj); err != nil {
			slog.Error("harvest: undecodable message, skipping", "subject", subject, "err", err)
			return
		}
		select {
		case ch <- obj:
		case <-ctx.Done():
		}
	})
	if err != nil {
		return nil, fmt.Errorf("harvest: consume %s: %w", subject, err)
	}

	go func() {
		<-ctx.Done()
		cc.Stop()
	}()

	return ch, nil
}
