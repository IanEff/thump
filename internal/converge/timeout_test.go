package converge_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/ianeff/thump/internal/converge"
)

// timedOutTransport answers every request the way a bounded client answers a
// stalled backend: with the deadline error, never with a hang.
type timedOutTransport struct{}

func (timedOutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

// TestProber_ATimedOutProbeIsNotConverged pins the asymmetry Converged's own
// doc comment names: a false "not converged" costs a reversal against a
// healthy system, a false "converged" strands an incident with its undo
// skipped. A timeout must land on the cheap side.
func TestProber_ATimedOutProbeIsNotConverged(t *testing.T) {
	t.Parallel()
	p := &converge.Prober{
		BaseURL: "http://prometheus.invalid",
		Client:  &http.Client{Transport: timedOutTransport{}},
		Queries: map[string]string{"ceph_health": "dummy_query"},
	}

	if p.Converged(t.Context(), "ceph_health", "< 1") {
		t.Error("a timed-out probe must not report convergence")
	}
}
