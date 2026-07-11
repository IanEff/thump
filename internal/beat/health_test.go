package beat_test

import (
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
