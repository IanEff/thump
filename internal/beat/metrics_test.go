package beat_test

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/ianeff/thump/internal/beat"
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
	_, _, shutdown := beat.Metrics("dummy-beat")
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
