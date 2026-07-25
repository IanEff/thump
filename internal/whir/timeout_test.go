package whir_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/whir"
)

// timedOutTransport answers every request the way a bounded client answers a
// stalled backend: with the deadline error, never with a hang.
type timedOutTransport struct{}

func (timedOutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

// TestResolver_ATimedOutQueryResolvesToUnknown pins the fail-closed half of
// the fix. State has no error return — every transport failure becomes
// StateUnknown — so the only thing a timeout can change here is whether the
// call comes back at all.
func TestResolver_ATimedOutQueryResolvesToUnknown(t *testing.T) {
	t.Parallel()
	r := &whir.Resolver{
		BaseURL: "http://prometheus.invalid",
		Client:  &http.Client{Transport: timedOutTransport{}},
		Queries: map[string]string{"rgw": "dummy_query"},
	}

	if diff := cmp.Diff(whir.StateUnknown, r.State(t.Context(), "rgw")); diff != "" {
		t.Error("a timed-out dependency query must resolve to unknown (-want +got)", diff)
	}
}
