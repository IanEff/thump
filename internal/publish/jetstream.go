package publish

import (
	"context"
	"fmt"

	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
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
	if _, err := p.js.Publish(ctx, subject, data); err != nil {
		return fmt.Errorf("jet publisher: publish %s: %w", subject, err)
	}
	return nil
}
