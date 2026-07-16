package converge_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ianeff/thump/internal/converge"
)

// fakePrometheus serves one canned instant-query response — the same
// data.result[0].value[1] shape internal/whir/resolve.go already reads.
// value is the literal string Prometheus would send (e.g. "0" or "220.4");
// an empty value serves an empty result vector instead, the "no data"
// response a real query gives for e.g. an unscraped metric.
func fakePrometheus(t *testing.T, value string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		result := fmt.Sprintf(`[{"metric":{},"value":[1700000000,%q]}]`, value)
		if value == "" {
			result = "[]"
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, result)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProber_ConvergedComparesLiveValueAgainstTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		metricVal     string // what the fake Prometheus reports for the query
		metric        string // SuccessCriteria.Metric — must be a key in Queries below
		target        string // SuccessCriteria.Target prose
		wantConverged bool
	}{
		{
			name: "latency under target converges", metricVal: "220.4",
			metric: "latency_p99", target: "p99 < 250ms", wantConverged: true,
		},
		{
			name: "latency over target does not converge", metricVal: "310.0",
			metric: "latency_p99", target: "p99 < 250ms", wantConverged: false,
		},
		{
			name: "latency exactly at target does not converge", metricVal: "250.0",
			metric: "latency_p99", target: "p99 < 250ms", wantConverged: false,
		},
		{
			name: "ceph HEALTH_OK converges when the value is 0", metricVal: "0",
			metric: "ceph_health", target: "HEALTH_OK", wantConverged: true,
		},
		{
			name: "ceph HEALTH_OK does not converge on WARN", metricVal: "1",
			metric: "ceph_health", target: "HEALTH_OK", wantConverged: false,
		},
		{
			name: "an empty result vector fails closed", metricVal: "",
			metric: "latency_p99", target: "p99 < 250ms", wantConverged: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := fakePrometheus(t, tc.metricVal)
			p := &converge.Prober{
				BaseURL: srv.URL,
				Queries: map[string]string{
					"latency_p99": "some_p99_query",
					"ceph_health": "ceph_health_status",
				},
			}

			got := p.Converged(context.Background(), tc.metric, tc.target)

			if got != tc.wantConverged {
				t.Errorf("Converged(%q, %q) with live value %q = %v, want %v",
					tc.metric, tc.target, tc.metricVal, got, tc.wantConverged)
			}
		})
	}
}

func TestProber_ConvergedFailsClosedWhenTheMetricIsUnbound(t *testing.T) {
	t.Parallel()
	srv := fakePrometheus(t, "0")
	p := &converge.Prober{BaseURL: srv.URL, Queries: map[string]string{}} // no entry for "latency_p99"

	if p.Converged(context.Background(), "latency_p99", "p99 < 250ms") {
		t.Error("a metric with no bound query must not converge silently-true")
	}
}

func TestProber_ConvergedFailsClosedOnAnUnparseableTarget(t *testing.T) {
	t.Parallel()
	srv := fakePrometheus(t, "0")
	p := &converge.Prober{BaseURL: srv.URL, Queries: map[string]string{"latency_p99": "some_p99_query"}}

	if p.Converged(context.Background(), "latency_p99", "somewhere between fine and not") {
		t.Error("an unparseable target must not converge silently-true")
	}
}

func TestProber_ConvergedFailsClosedWhenPrometheusIsUnreachable(t *testing.T) {
	t.Parallel()
	srv := fakePrometheus(t, "0")
	srv.Close() // torn down immediately: BaseURL now points at nothing listening

	p := &converge.Prober{BaseURL: srv.URL, Queries: map[string]string{"latency_p99": "some_p99_query"}}

	if p.Converged(context.Background(), "latency_p99", "p99 < 250ms") {
		t.Error("an unreachable Prometheus must not converge silently-true — a network failure isn't proof of recovery")
	}
}

func TestProber_ConvergedFailsClosedOnANon200Response(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	p := &converge.Prober{BaseURL: srv.URL, Queries: map[string]string{"latency_p99": "some_p99_query"}}

	if p.Converged(context.Background(), "latency_p99", "p99 < 250ms") {
		t.Error("a non-200 Prometheus response must not converge silently-true")
	}
}
