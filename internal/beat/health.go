package beat

import (
	"net/http"
	"sync/atomic"
)

// Health is a beat's /healthz + /readyz surface for liveness indicators.
type Health struct {
	ready atomic.Bool
}

// SetReady flips the /readyz verdict.
func (h *Health) SetReady(ready bool) {
	h.ready.Store(ready)
}

// Livez answers 200 unconditionally.  Proves process up.
func (h *Health) Livez(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// Readyz answers 503 until SetReady(true) has been called.
func (h *Health) Readyz(w http.ResponseWriter, _ *http.Request) {
	if !h.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
