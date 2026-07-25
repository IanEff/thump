// Package httpx is the one place an outbound HTTP client is built.
package httpx

import (
	"net/http"
	"time"
)

// DefaultBackendTimeout bounds one call to a telemetry backend. Sized for a
// Prometheus range query over a 15-minute window, not for a healthy p99 — a
// call that needs longer than this is a backend in trouble, and saying so
// beats waiting on it.
const DefaultBackendTimeout = 10 * time.Second

// Client returns a client whose every call is bounded by a timeout.
// A zero timeout is not special-cased away.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}
