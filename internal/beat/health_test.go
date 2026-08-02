package beat_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/health"
)

// TestBrokerHooks_DrivesReadinessThroughADropAndRecovery pins the property
// the ghosting bug violated: a beat whose broker is gone must stop reporting
// itself ready, and must start again when the broker returns — without a
// restart in between.
func TestBrokerHooks_DrivesReadinessThroughADropAndRecovery(t *testing.T) {
	t.Parallel()
	h := &health.Health{}
	h.SetReady(true)
	hooks := beat.BrokerHooks(h, "dummy beat", nil)

	hooks.OnDisconnect(errors.New("dummy drop"))
	if diff := cmp.Diff(http.StatusServiceUnavailable, readyzStatus(t, h)); diff != "" {
		t.Error("a beat with no broker still reported itself ready", diff)
	}

	hooks.OnReconnect()
	if diff := cmp.Diff(http.StatusOK, readyzStatus(t, h)); diff != "" {
		t.Error("a beat whose broker came back never reported itself ready again", diff)
	}
}

// TestBrokerHooks_OnClosedRunsTheCallerSExitHook pins that a terminally lost
// connection reaches the beat, which is what turns it into a non-zero exit
// rather than a pod idling forever.
func TestBrokerHooks_OnClosedRunsTheCallerSExitHook(t *testing.T) {
	t.Parallel()
	h := &health.Health{}
	h.SetReady(true)
	var closed bool
	hooks := beat.BrokerHooks(h, "dummy beat", func() { closed = true })

	hooks.OnClosed()

	if !closed {
		t.Error("OnClosed never reached the beat — a permanently disconnected beat would sit Running forever")
	}
	if diff := cmp.Diff(http.StatusServiceUnavailable, readyzStatus(t, h)); diff != "" {
		t.Error("a beat with a closed connection still reported itself ready", diff)
	}
}

func readyzStatus(t *testing.T, h *health.Health) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code
}
