package publish

import (
	"context"
	"fmt"

	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/propagation"
)

// JetPublisher is the JetStream Publisher: it marshals obj through
// internal/wire and publishes it to subject, with no local durability of
// its own — WALPublisher supplies that leg when the caller needs it.
type JetPublisher[T any] struct {
	js jetstream.JetStream
}

// NewJetPublisher builds a JetPublisher over an already-connected
// JetStream context (see broker.Connect).
func NewJetPublisher[T any](js jetstream.JetStream) *JetPublisher[T] {
	return &JetPublisher[T]{js: js}
}

// Publish marshals obj (internal/wire) and publishes it to subject.
func (p *JetPublisher[T]) Publish(ctx context.Context, subject string, obj T) error {
	data, err := wire.Marshal(obj)
	if err != nil {
		return fmt.Errorf("jet publisher: marshal %s: %w", subject, err)
	}

	header := make(nats.Header)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(header))

	msg := &nats.Msg{Subject: subject, Data: data, Header: header}

	if _, err := p.js.PublishMsg(ctx, msg); err != nil {
		return fmt.Errorf("jet publisher: publish %s: %w", subject, err)
	}
	return nil
}
