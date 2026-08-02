package health_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/health"
)

// TestHealth_HandlersReportStatus pins Livez as unconditional (a beat that's
// up answers 200 regardless of readiness) and Readyz as gated on SetReady —
// the contract a rollout's probes actually depend on.
func TestHealth_HandlersReportStatus(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		ready   bool
		handler func(*health.Health) http.HandlerFunc
		want    int
	}{
		"Livez reports 200 when not ready": {
			ready:   false,
			handler: func(h *health.Health) http.HandlerFunc { return h.Livez },
			want:    http.StatusOK,
		},
		"Livez reports 200 when ready": {
			ready:   true,
			handler: func(h *health.Health) http.HandlerFunc { return h.Livez },
			want:    http.StatusOK,
		},
		"Readyz reports 503 when not ready": {
			ready:   false,
			handler: func(h *health.Health) http.HandlerFunc { return h.Readyz },
			want:    http.StatusServiceUnavailable,
		},
		"Readyz reports 200 when ready": {
			ready:   true,
			handler: func(h *health.Health) http.HandlerFunc { return h.Readyz },
			want:    http.StatusOK,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			h := &health.Health{}
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
	h := &health.Health{}

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
	h := &health.Health{}

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

// TestReadyz_CarriesTheReasonSoAnOperatorLearnsWhichDependencyIsGone pins the
// body, not just the code — a 503 with no reason sends the reader to the logs.
func TestReadyz_CarriesTheReasonSoAnOperatorLearnsWhichDependencyIsGone(t *testing.T) {
	t.Parallel()
	h := &health.Health{}
	h.NotReady("broker unreachable")

	rec := httptest.NewRecorder()
	h.Readyz(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if diff := cmp.Diff("broker unreachable", rec.Body.String()); diff != "" {
		t.Error("wrong /readyz body for an unreachable broker", diff)
	}
}
