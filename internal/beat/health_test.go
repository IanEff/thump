package beat_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/beat"
)

// TestHealth_HandlersReportStatus pins Livez as unconditional (a beat that's
// up answers 200 regardless of readiness) and Readyz as gated on SetReady —
// the contract a rollout's probes actually depend on.
func TestHealth_HandlersReportStatus(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		ready   bool
		handler func(*beat.Health) http.HandlerFunc
		want    int
	}{
		"Livez reports 200 when not ready": {
			ready:   false,
			handler: func(h *beat.Health) http.HandlerFunc { return h.Livez },
			want:    http.StatusOK,
		},
		"Livez reports 200 when ready": {
			ready:   true,
			handler: func(h *beat.Health) http.HandlerFunc { return h.Livez },
			want:    http.StatusOK,
		},
		"Readyz reports 503 when not ready": {
			ready:   false,
			handler: func(h *beat.Health) http.HandlerFunc { return h.Readyz },
			want:    http.StatusServiceUnavailable,
		},
		"Readyz reports 200 when ready": {
			ready:   true,
			handler: func(h *beat.Health) http.HandlerFunc { return h.Readyz },
			want:    http.StatusOK,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			h := &beat.Health{}
			h.SetReady(tc.ready)

			rec := httptest.NewRecorder()
			tc.handler(h)(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			if diff := cmp.Diff(tc.want, rec.Code); diff != "" {
				t.Error("wrong status code", diff)
			}
		})
	}
}

// TestHealth_ZeroValueIsNotReady pins fail-closed as the default: a Health
// that never had SetReady called must not be mistaken for a table case
// where SetReady(false) was called explicitly — the zero value itself is
// the contract a forgetful Main relies on.
func TestHealth_ZeroValueIsNotReady(t *testing.T) {
	h := &beat.Health{}

	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if diff := cmp.Diff(http.StatusServiceUnavailable, rec.Code); diff != "" {
		t.Error("wrong status code for a Health with SetReady never called", diff)
	}
}

// TestHealth_ReadyzUnderConcurrentSetReadyIsRaceFree exercises the real
// production shape — the broker-connect handshake goroutine calling
// SetReady while an HTTP probe goroutine calls Readyz. Only meaningful
// under -race; it's the proof Health needs an atomic.Bool, not a plain bool.
func TestHealth_ReadyzUnderConcurrentSetReadyIsRaceFree(t *testing.T) {
	t.Parallel()
	h := &beat.Health{}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.SetReady(true)
	}()
	go func() {
		defer wg.Done()
		rec := httptest.NewRecorder()
		h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	wg.Wait()
}

// TestBrokerHooks_DrivesReadinessThroughADropAndRecovery pins the property
// the ghosting bug violated: a beat whose broker is gone must stop reporting
// itself ready, and must start again when the broker returns — without a
// restart in between.
func TestBrokerHooks_DrivesReadinessThroughADropAndRecovery(t *testing.T) {
	t.Parallel()
	h := &beat.Health{}
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
	h := &beat.Health{}
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

// TestReadyz_CarriesTheReasonSoAnOperatorLearnsWhichDependencyIsGone pins the
// body, not just the code — a 503 with no reason sends the reader to the logs.
func TestReadyz_CarriesTheReasonSoAnOperatorLearnsWhichDependencyIsGone(t *testing.T) {
	t.Parallel()
	h := &beat.Health{}
	h.NotReady("broker unreachable")

	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if diff := cmp.Diff("broker unreachable", rec.Body.String()); diff != "" {
		t.Error("wrong /readyz body for an unreachable broker", diff)
	}
}

func readyzStatus(t *testing.T, h *beat.Health) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Code
}
