package clank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

// timedOutTransport answers every request the way a bounded client answers a
// stalled backend: with the deadline error, never with a hang.
type timedOutTransport struct{}

func (timedOutTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

// TestMetricsTool_ATimedOutQueryReturnsNonLiveEvidence pins what the reason
// loop sees when Prometheus stalls. Run reports backend failure as evidence,
// never as an error, so the model gets a citable "this didn't answer" and the
// gate's live-citation floor declines on it — which only works if the call
// returns at all.
func TestMetricsTool_ATimedOutQueryReturnsNonLiveEvidence(t *testing.T) {
	t.Parallel()
	tool := &clank.MetricsTool{
		BaseURL: "http://prometheus.invalid",
		Client:  &http.Client{Transport: timedOutTransport{}},
		Queries: map[string]string{"ceph_health": "dummy_promql"},
	}

	got, err := tool.Run(t.Context(), json.RawMessage(`{"q":"ceph_health"}`))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got.Summary, "prometheus request failed") {
		t.Errorf("the summary must name the failure so the model can cite it, got %q", got.Summary)
	}
	got.Summary = "" // asserted above; the transport's wording is the stdlib's, not this test's to pin
	want := proposal.EvidenceRef{Tool: "metrics", Query: "ceph_health", Live: false}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("a timed-out metrics query must come back as non-live evidence (-want +got)", diff)
	}
}
