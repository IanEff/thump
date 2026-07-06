package publish

import (
	"context"
	"fmt"

	"github.com/ianeff/thump/internal/wire"
	"github.com/nats-io/nats.go/jetstream"
)

type JetPublisher[T any] struct {
	js jetstream.JetStream
}

func NewJetPublisher[T any](js jetstream.JetStream) *JetPublisher[T] {
	return &JetPublisher[T]{js: js}
}

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
