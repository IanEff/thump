package broker_test

import (
	"context"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
)

func TestConnect_ReturnsAReadyJetStreamAndAnIdempotentClose(t *testing.T) {
	t.Parallel()
	url := natstest.URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	js, closeNC, err := broker.Connect(ctx, url)
	if err != nil {
		t.Fatal("connect:", err)
	}
	defer closeNC()

	if _, err := js.Stream(ctx, broker.StreamName); err != nil {
		t.Fatal("Connect didn't ensure the topology — stream missing:", err)
	}

	closeNC() // idempotent: a second call must not panic or hang
}

func TestConnect_FailsOnABadURL(t *testing.T) {
	t.Parallel()
	if _, _, err := broker.Connect(context.Background(), "nats://127.0.0.1:1"); err == nil {
		t.Fatal("expected an error connecting to a closed port")
	}
}
