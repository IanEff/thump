package beat_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/tlsx"
	"github.com/ianeff/thump/internal/tlsxtest"
)

// recordSink hands every record to a channel, so a test can wait on a line a
// background goroutine emits rather than poll a buffer that goroutine is
// concurrently writing. The send is non-blocking: the listener goroutine must
// never block on a test that has stopped reading.
type recordSink struct{ records chan slog.Record }

func (s recordSink) Enabled(context.Context, slog.Level) bool { return true }
func (s recordSink) WithAttrs([]slog.Attr) slog.Handler       { return s }
func (s recordSink) WithGroup(string) slog.Handler            { return s }

func (s recordSink) Handle(_ context.Context, r slog.Record) error {
	select {
	case s.records <- r:
	default:
	}
	return nil
}

// attr returns the value r carries under key, so an assertion reads a
// structured field instead of substring-matching a rendered line.
func attr(r slog.Record, key string) (string, bool) {
	var got string
	var found bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			got, found = a.Value.String(), true
			return false
		}
		return true
	})
	return got, found
}

// TestMetrics_ABindFailureSaysSoOnTheWayDown pins the loudest silent failure
// in the kit. One mux serves /metrics, /healthz and /readyz, so a refused
// bind costs the scrape surface and both probes at once; the beat keeps
// running either way, and what this test forbids is doing it quietly.
//
// slog.SetDefault and t.Setenv are both process-global, so this test must
// never call t.Parallel() — Go runs every non-parallel test to completion
// before any parallel one resumes, and that ordering is what keeps it safe
// under -race alongside this package's parallel tests.
func TestMetrics_ABindFailureSaysSoOnTheWayDown(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })

	sink := recordSink{records: make(chan slog.Record, 8)}
	prev := slog.Default()
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(prev) })

	t.Setenv("METRICS_ADDR", occupied.Addr().String())
	_, _, shutdown := beat.Metrics("dummy-beat", nil)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	select {
	case r := <-sink.records:
		if r.Message != "metrics listener stopped" {
			t.Errorf("first line was %q, want the listener's own stop line", r.Message)
		}
		if r.Level != slog.LevelError {
			t.Errorf("a dead health surface logged at %v, want error", r.Level)
		}
		got, ok := attr(r, "addr")
		if !ok || got != occupied.Addr().String() {
			t.Errorf("the stop line must name the address that failed, got %q (present=%v)", got, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the metrics listener failed to bind and said nothing")
	}
}

// TestMetrics_TLSConfigured_ServesHealthzOverTLSAndRefusesPlaintext pins R7a:
// a non-nil tlsCfg switches the listener to ListenAndServeTLS rather than
// opening a second plaintext port beside it — there is one address, and a
// client without the right certificate gets nothing from it.
//
// t.Setenv makes this test ineligible for t.Parallel(), same as
// TestMetrics_ABindFailureSaysSoOnTheWayDown above.
func TestMetrics_TLSConfigured_ServesHealthzOverTLSAndRefusesPlaintext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	ca := tlsxtest.NewCA(t)
	leaf := ca.Leaf(t, "metrics", tlsxtest.IPSAN(net.ParseIP("127.0.0.1")))
	serverTLS, err := tlsx.Server(leaf)
	if err != nil {
		t.Fatalf("tlsx.Server: %v", err)
	}
	clientTLS, err := tlsx.Client(leaf)
	if err != nil {
		t.Fatalf("tlsx.Client: %v", err)
	}
	clientTLS.ServerName = "metrics"

	t.Setenv("METRICS_ADDR", addr)
	_, _, shutdown := beat.Metrics("dummy-beat", serverTLS)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	httpsClient := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = httpsClient.Get("https://" + addr + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET https://%s/healthz: %v", addr, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("TLS request to /healthz returned %d, want 200", resp.StatusCode)
	}

	// net/http's server detects a plaintext request on a TLS listener and
	// answers 400 rather than dropping the connection — so the plain client
	// gets a response, just never the 200 a real scrape needs.
	plainClient := &http.Client{Timeout: time.Second}
	plainResp, err := plainClient.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("plaintext GET http://%s/healthz: %v", addr, err)
	}
	_ = plainResp.Body.Close()
	if plainResp.StatusCode == http.StatusOK {
		t.Error("a plaintext GET against a TLS-configured metrics port returned 200, want refused")
	}
}
