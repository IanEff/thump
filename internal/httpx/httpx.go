// Package httpx is the one place an outbound HTTP client is built.
package httpx

import (
	"crypto/tls"
	"net/http"
	"time"
)

// DefaultBackendTimeout bounds one call to a telemetry backend. Sized for a
// Prometheus range query over a 15-minute window, not for a healthy p99 — a
// call that needs longer than this is a backend in trouble, and saying so
// beats waiting on it.
const DefaultBackendTimeout = 10 * time.Second

// Client returns a client whose every call is bounded by a timeout, dialing
// over tlsCfg when it isn't nil. A zero timeout is not special-cased away.
// A nil tlsCfg leaves the transport at its default, so a caller with no
// private CA to verify a peer against keeps today's plaintext behavior.
func Client(timeout time.Duration, tlsCfg *tls.Config) *http.Client {
	c := &http.Client{Timeout: timeout}
	if tlsCfg != nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = tlsCfg
		c.Transport = t
	}
	return c
}
