// Package health is a beat's /healthz + /readyz surface for liveness
// indicators — pure net/http and sync/atomic, no transport or beat
// dependency of its own, so every beat and internal/beat's own
// BrokerHooks/AwaitConsumers can depend on it without a cycle.
package health

import (
	"io"
	"net/http"
	"sync/atomic"
)

// Health is a beat's /healthz + /readyz surface for liveness indicators.
type Health struct {
	ready  atomic.Bool
	reason atomic.Pointer[string]
}

// SetReady flips the /readyz verdict, clearing any reason a prior NotReady
// recorded.
func (h *Health) SetReady(ready bool) {
	if ready {
		h.reason.Store(nil)
	}
	h.ready.Store(ready)
}

// NotReady flips /readyz to 503 and records why — an operator who curls a
// failing probe should learn which dependency is gone, not just that one is.
func (h *Health) NotReady(reason string) {
	h.reason.Store(&reason)
	h.ready.Store(false)
}

// Livez answers 200 unconditionally.  Proves process up.
func (h *Health) Livez(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// Readyz answers 503 until SetReady(true) has been called, carrying
// NotReady's reason as the body when one was recorded.
func (h *Health) Readyz(w http.ResponseWriter, _ *http.Request) {
	if !h.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		if reason := h.reason.Load(); reason != nil {
			_, _ = io.WriteString(w, *reason)
		}
		return
	}
	w.WriteHeader(http.StatusOK)
}
