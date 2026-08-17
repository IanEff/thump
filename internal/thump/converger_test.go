package thump_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/thump"
)

type fakeProbe struct {
	answer            bool
	severity          float64
	severityOK        bool
	gotMetric, gotTgt string
}

func (f *fakeProbe) Converged(_ context.Context, metric, target string) bool {
	f.gotMetric, f.gotTgt = metric, target
	return f.answer
}

func (f *fakeProbe) Severity(_ context.Context, _ string) (float64, bool) {
	return f.severity, f.severityOK
}

func TestPrometheusConverger_UnpacksOrderSuccessIntoTheProbe(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{answer: true}
	c := thump.PrometheusConverger{Probe: probe}

	converged, _ := c.Settle(context.Background(), goldenOrder())

	if !converged {
		t.Error("PrometheusConverger must return the probe's own answer")
	}
	if probe.gotMetric != "latency_p99" || probe.gotTgt != "p99 < 250ms" {
		t.Errorf("probe got (%q, %q), want the order's Success.Metric/.Target", probe.gotMetric, probe.gotTgt)
	}
}

type queryFakeProbe struct {
	severities map[string]float64
	gotQueries []string
	converged  bool
}

func (q *queryFakeProbe) Converged(_ context.Context, _, _ string) bool {
	return q.converged
}

func (q *queryFakeProbe) Severity(_ context.Context, query string) (float64, bool) {
	q.gotQueries = append(q.gotQueries, query)
	v, ok := q.severities[query]
	return v, ok
}

// TestSettle_ThePostActionReadingComesFromTheSLOThatFiredNotTheActionsOwnChoice
// pins the authorship hole PR 2's artifact exposes: severityQuery is authored
// on the ActionContract, so the number the outcome is judged by is chosen by
// the same author as the action being judged. The SLO rattle detected on is
// the one value in the chain neither the model nor a catalog author picks.
func TestSettle_ThePostActionReadingComesFromTheSLOThatFiredNotTheActionsOwnChoice(t *testing.T) {
	t.Parallel()

	probe := &queryFakeProbe{
		converged: true,
		severities: map[string]float64{
			"cart-availability":          0.015,
			"severity_cart_availability": 0.950,
		},
	}
	c := thump.PrometheusConverger{Probe: probe}

	o := thump.Order{
		ID:        "ord:slo_burn:cart:1000",
		SignalRef: "slo_burn:cart",
		SLORef:    "cart-availability", // the SLO that fired
		Success: contract.SuccessCriteria{
			Metric:        "cart_error_ratio",
			Target:        "cart_error_ratio < 0.02",
			SeverityQuery: "severity_cart_availability", // the author's choice
		},
	}

	converged, severity := c.Settle(context.Background(), o)

	if !converged {
		t.Fatal("Settle must report convergence from the probe")
	}
	if severity == nil {
		t.Fatal("Settle must return a measured severity reading")
	}
	if diff := cmp.Diff(0.015, *severity); diff != "" {
		t.Errorf("severity mismatch: wanted reading from fired SLO cart-availability (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"cart-availability"}, probe.gotQueries); diff != "" {
		t.Errorf("probed queries mismatch (-want +got):\n%s", diff)
	}
}
