package httpx_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/internal/httpx"
)

// stallTransport never answers, blocking until the request's context is done
// — what a backend that accepts the connection and then wedges looks like
// from the client side.
type stallTransport struct{}

func (stallTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// TestClient_EndsACallTheBackendNeverAnswers is the whole reason this package
// exists: the failure it pins is a call that returns nothing and never
// returns at all. The elapsed-time assertion is exact rather than
// approximate because synctest's clock only advances once every goroutine in
// the bubble is blocked.
func TestClient_EndsACallTheBackendNeverAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := httpx.Client(httpx.DefaultBackendTimeout)
		c.Transport = stallTransport{}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
			"http://stalled.invalid/api/v1/query", nil)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()

		_, err = c.Do(req)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a stalled backend must end in a deadline error, got %v", err)
		}
		if got := time.Since(start); got != httpx.DefaultBackendTimeout {
			t.Errorf("call ran %v, want it cut at the configured %v", got, httpx.DefaultBackendTimeout)
		}
	})
}
