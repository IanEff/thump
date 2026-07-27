// Package natstest spins up an embedded NATS+JetStream server for tests. It is
// imported only from _test.go files across the module — never from production
// code — so the real nats-server binary never links into a shipped binary.
package natstest

import (
	"crypto/tls"
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

// SecureOptions is the embedded server's mTLS shape for a handshake test:
// TLSConfig is the server's own tlsx.Server config, and Users maps a SAN
// email to its permissions the same way nats.conf's verify_and_map does in
// the shipped chart (deploy/chart/thump/templates/nats.yaml) — a cert's
// identity is checked against Username, never a password.
type SecureOptions struct {
	TLSConfig *tls.Config
	Users     []*natssrv.User
}

// SecureURL starts the same embedded server as URL, but requiring a client
// certificate and mapping the connecting cert's SAN email against
// opts.Users, so a test can prove broker.Connect's mTLS wiring — and a
// permission table bound to it — without a rendered chart. Bound to a fixed
// loopback address (not the default 0.0.0.0) so opts.TLSConfig's server leaf
// can carry a matching IP SAN (tlsxtest.IPSAN) — x509 verification checks a
// dialed IP against IPAddresses and never falls back to DNSNames.
func SecureURL(t *testing.T, opts SecureOptions) string {
	t.Helper()
	srv, err := natssrv.NewServer(&natssrv.Options{
		Host:      "127.0.0.1",
		Port:      -1, // random free port
		JetStream: true,
		StoreDir:  t.TempDir(),
		TLSConfig: opts.TLSConfig,
		TLSMap:    true,
		Users:     opts.Users,
	})
	if err != nil {
		t.Fatal("embedded secure nats:", err)
	}

	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("embedded secure nats not ready")
	}
	t.Cleanup(srv.Shutdown)

	return srv.ClientURL()
}
