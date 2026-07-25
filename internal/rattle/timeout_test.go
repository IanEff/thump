package rattle_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ianeff/thump/internal/rattle"
)

// timedOutTransport answers every request the way a bounded client answers a
// stalled backend: with the deadline error, never with a hang.
type timedOutTransport struct{}

func (timedOutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

// TestPromSource_BurnSamplesReportsATimeoutRatherThanHanging pins the fix at
// rattle's only live-backend read. Reconcile issues one of these per SLO in
// sequence and aborts the tick on the first error, so a call that never
// returns stops rattle detecting for every SLO, not just this one.
func TestPromSource_BurnSamplesReportsATimeoutRatherThanHanging(t *testing.T) {
	t.Parallel()
	src := &rattle.PromSource{
		BaseURL: "http://prometheus.invalid",
		Client:  &http.Client{Transport: timedOutTransport{}},
	}

	_, err := src.BurnSamples(t.Context(), rattle.SLO{ID: "dummy-slo"})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a stalled Prometheus must surface as a deadline error, got %v", err)
	}
}
