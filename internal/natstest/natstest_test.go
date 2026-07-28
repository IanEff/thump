package natstest_test

import (
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/natstest"
	"github.com/nats-io/nats.go"
)

// TestStart_ReturnsServerThatAnswersOnURL asserts that Start spins up a NATS server
// listening on URL that responds with the NATS protocol INFO banner on TCP dial.
func TestStart_ReturnsServerThatAnswersOnURL(t *testing.T) {
	t.Parallel()

	b := natstest.Restartable(t)

	u, err := url.Parse(b.URL())
	if err != nil {
		t.Fatalf("parse Bouncer URL %q: %v", b.URL(), err)
	}

	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if err != nil {
		t.Fatalf("dial NATS server at %s: %v", u.Host, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetReadDeadline((time.Now().Add(2 * time.Second))); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read NATS banner: %v", err)
	}

	got := string(buf[:n])
	if !strings.HasPrefix(got, "INFO ") {
		t.Fatalf("expected NATS INFO banner prefix, got %q", got)
	}
}

// TestRestartable_SurvivesRestartOnSamePort asserts that a Bouncer server can be stopped
// and restarted, binding the same port and accepting new NATS connections.
func TestRestartable_SurvivesRestartOnSamePort(t *testing.T) {
	t.Parallel()

	b := natstest.Restartable(t)

	nc1, err := nats.Connect(b.URL())
	if err != nil {
		t.Fatalf("initial NATS connect failed: %v", err)
	}
	nc1.Close()

	b.Stop()

	// Verify server port is stopped and rejected
	u, err := url.Parse(b.URL())
	if err != nil {
		t.Fatalf("parse Bouncer URL %q: %v", b.URL(), err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected TCP dial to fail while server is stopped")
	}

	b.Start()

	nc2, err := nats.Connect(b.URL())
	if err != nil {
		t.Fatalf("reconnect after restart failed: %v", err)
	}
	defer nc2.Close()

	if !nc2.IsConnected() {
		t.Fatal("NATS client is not connected after server restart")
	}
}

// TestURL_StableAcrossRestartableLifecycle asserts that Bouncer.URL remains constant
// across Stop and Start cycles.
func TestURL_StableAcrossRestartableLifecycle(t *testing.T) {
	t.Parallel()

	b := natstest.Restartable(t)

	initialURL := b.URL()
	if initialURL == "" {
		t.Fatal("initial Bouncer URL is empty")
	}

	b.Stop()
	stoppedURL := b.URL()
	if got, want := stoppedURL, initialURL; got != want {
		t.Errorf("URL changed while stopped: want %q, got %q", want, got)
	}

	b.Start()
	restartedURL := b.URL()
	if got, want := restartedURL, initialURL; got != want {
		t.Errorf("URL changed after restart: want %q, got %q", want, got)
	}
}
