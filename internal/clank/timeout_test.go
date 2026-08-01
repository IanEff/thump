package clank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/natstest"
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

func TestModelRequestTimeout_ExceedsAckWaitAndIsCoveredByTheSubscriberHeartbeat(t *testing.T) {
	t.Parallel()
	// The deliberate inversion, and the one nobody can check today. A reason
	// loop runs well past AckWait, which is safe only because Handler's
	// heartbeat resets the deadline on real checkpoint progress. This test
	// pins both halves together: drop the heartbeat and the relationship
	// silently becomes up to maxDeliver concurrent paid reason loops for one
	// detection.
	ctx := t.Context()
	ackWait, err := broker.ProvisionedAckWait(ctx, natstest.New(t), "thump.detections")
	if err != nil {
		t.Fatal(err)
	}
	if got := clank.ModelRequestTimeoutForTest(); got <= ackWait {
		t.Errorf("modelRequestTimeout is %s against a provisioned AckWait of %s — if the model call now fits inside AckWait, the heartbeat this beat depends on is no longer load-bearing and this test is asserting the wrong thing", got, ackWait)
	}
}
