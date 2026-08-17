package whir_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/whir"
)

func TestGraphSource_DerivesEachEdgesTrafficShareFromTheServiceGraphCounters(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		requestsPayload string
		failedPayload   string
		client          string
		wantEdges       []whir.Edge
	}{
		"DerivesEachEdgesTrafficShare computes proportional traffic shares and zero error rates for healthy dependencies": {
			client: "checkout",
			requestsPayload: `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"client":"checkout","server":"cart"},"value":[1688745600,"80.0"]},
				{"metric":{"client":"checkout","server":"payment"},"value":[1688745600,"20.0"]}
			]}}`,
			failedPayload: `{"status":"success","data":{"resultType":"vector","result":[]}}`,
			wantEdges: []whir.Edge{
				{Client: "checkout", Server: "cart", Share: 0.8, FailRate: 0.0, State: whir.StateHealthy},
				{Client: "checkout", Server: "payment", Share: 0.2, FailRate: 0.0, State: whir.StateHealthy},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				q := r.URL.Query().Get("query")
				switch {
				case strings.Contains(q, "request_total"):
					_, _ = w.Write([]byte(tc.requestsPayload))
				case strings.Contains(q, "request_failed_total"):
					_, _ = w.Write([]byte(tc.failedPayload))
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer srv.Close()

			g := &whir.GraphSource{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Window:  5 * time.Minute,
				Queries: whir.GraphQueries{
					Requests: `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
					Failed:   `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
				},
			}

			got, err := g.Edges(context.Background(), tc.client)
			if err != nil {
				t.Fatalf("unexpected error fetching edges: %v", err)
			}
			if diff := cmp.Diff(tc.wantEdges, got); diff != "" {
				t.Errorf("wrong edges (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGraphSource_AServiceThatEmitsNoTracesHasNoObservedEdgesAndNoError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		client    string
		wantEdges []whir.Edge
	}{
		"AServiceThatEmitsNoTraces returns nil edges and nil error when the Prometheus query returns an empty result": {
			client:    "acme-api",
			wantEdges: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			}))
			defer srv.Close()

			g := &whir.GraphSource{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Window:  5 * time.Minute,
				Queries: whir.GraphQueries{
					Requests: `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
					Failed:   `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
				},
			}

			got, err := g.Edges(context.Background(), tc.client)
			if err != nil {
				t.Fatalf("unexpected error fetching edges: %v", err)
			}
			if diff := cmp.Diff(tc.wantEdges, got); diff != "" {
				t.Errorf("wrong edges (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGraphSource_AnEdgeWhoseFailedRatioClearsTheThresholdReportsDegraded(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		requestsPayload string
		failedPayload   string
		client          string
		wantEdges       []whir.Edge
	}{
		"AnEdgeWhoseFailedRatioClearsTheThreshold marks the high-error edge as degraded while healthy edges stay healthy": {
			client: "frontend",
			requestsPayload: `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"client":"frontend","server":"catalog"},"value":[1688745600,"50.0"]},
				{"metric":{"client":"frontend","server":"checkout"},"value":[1688745600,"50.0"]}
			]}}`,
			failedPayload: `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"client":"frontend","server":"checkout"},"value":[1688745600,"25.0"]}
			]}}`,
			wantEdges: []whir.Edge{
				{Client: "frontend", Server: "catalog", Share: 0.5, FailRate: 0.0, State: whir.StateHealthy},
				{Client: "frontend", Server: "checkout", Share: 0.5, FailRate: 0.5, State: whir.StateDegraded},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				q := r.URL.Query().Get("query")
				switch {
				case strings.Contains(q, "request_total"):
					_, _ = w.Write([]byte(tc.requestsPayload))
				case strings.Contains(q, "request_failed_total"):
					_, _ = w.Write([]byte(tc.failedPayload))
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer srv.Close()

			g := &whir.GraphSource{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Window:  5 * time.Minute,
				Queries: whir.GraphQueries{
					Requests:        `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
					Failed:          `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
					FailedThreshold: 0.05,
				},
			}

			got, err := g.Edges(context.Background(), tc.client)
			if err != nil {
				t.Fatalf("unexpected error fetching edges: %v", err)
			}
			if diff := cmp.Diff(tc.wantEdges, got); diff != "" {
				t.Errorf("wrong edges (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLoadGraphQueries_ParsesTheVersionedShapeTheRigConfigsShip pins the
// contract config/<rig>/whir/graph-queries.yaml relies on: a top-level
// version key alongside a nested queries map, the same shape LoadGraphQueries
// must parse for the file the chart mounts (deploy/chart/thump/templates/configmap-whir.yaml).
func TestLoadGraphQueries_ParsesTheVersionedShapeTheRigConfigsShip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		yaml string
		want whir.GraphQueries
	}{
		"ParsesTheVersionedShape reads queries nested under a version key, matching every shipped config/*/whir/graph-queries.yaml": {
			yaml: `
version: v1
queries:
  requests: 'sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))'
  failed: 'sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))'
  inboundRequests: 'sum by (client, server) (rate(traces_service_graph_request_total{server="%s"}[%s]))'
  inboundFailed: 'sum by (client, server) (rate(traces_service_graph_request_failed_total{server="%s"}[%s]))'
  failedThreshold: 0.05
`,
			want: whir.GraphQueries{
				Requests:        `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
				Failed:          `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
				InboundRequests: `sum by (client, server) (rate(traces_service_graph_request_total{server="%s"}[%s]))`,
				InboundFailed:   `sum by (client, server) (rate(traces_service_graph_request_failed_total{server="%s"}[%s]))`,
				FailedThreshold: 0.05,
			},
		},
		"ParsesTheDirectShape reads a bare queries map with no version wrapper": {
			yaml: `
requests: 'sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))'
failed: 'sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))'
`,
			want: whir.GraphQueries{
				Requests: `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
				Failed:   `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "graph-queries.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			got, err := whir.LoadGraphQueries(path)
			if err != nil {
				t.Fatalf("unexpected error loading graph queries: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("wrong graph queries (-want +got):\n%s", diff)
			}
		})
	}
}

// TestLoadGraphQueries_AMissingFileReturnsAnError pins the one real error
// path LoadGraphQueries has — unlike GraphSource.Edges, which never surfaces
// a query failure as an error, a config file that isn't there is an operator
// mistake and must fail loudly at startup.
func TestLoadGraphQueries_AMissingFileReturnsAnError(t *testing.T) {
	t.Parallel()

	_, err := whir.LoadGraphQueries(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("want error loading a missing graph queries file, got nil")
	}
}
