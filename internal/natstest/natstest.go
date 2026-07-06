// Package natstest spins up an embedded NATS+JetStream server for tests. It is
// imported only from _test.go files across the module — never from production
// code — so the real nats-server binary never links into a shipped binary.
package natstest

import (
	"testing"
	"time"

	natssrv "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// New starts an embedded NATS server with JetStream enabled inside the test
// process (no Docker, no network, no key) and returns a ready JetStream
// context. The server and connection are cleaned up via t.Cleanup.
func New(t *testing.T) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(URL(t))
	if err != nil {
		t.Fatal("connect:", err)
	}
	t.Cleanup(nc.Close)

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal("jetstream:", err)
	}
	return js
}

// URL starts the same embedded server as New, but returns its client URL
// instead of an already-connected JetStream context — for tests exercising
// code that does its own nats.Connect (e.g. broker.Connect).
func URL(t *testing.T) string {
	t.Helper()
	srv, err := natssrv.NewServer(&natssrv.Options{
		Port:      -1, // random free port
		JetStream: true,
		StoreDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal("embedded nats:", err)
	}

	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("embedded nats not ready")
	}
	t.Cleanup(srv.Shutdown)

	return srv.ClientURL()
}
