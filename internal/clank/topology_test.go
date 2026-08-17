package clank_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/subjects"
	"github.com/ianeff/thump/internal/whir"
)

func TestWhirTopology_ResolvesEdgesReachableFromTheSubject(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("query") {
		case "objectstore_query":
			_, _ = w.Write([]byte(`{"data":{"result":[{"value":[0,"1"]}]}}`))
		case "rook_query":
			_, _ = w.Write([]byte(`{"data":{"result":[{"value":[0,"0"]}]}}`))
		}
	}))
	defer srv.Close()

	cat, err := whir.Load([]byte(`
metadata:
  name: ceph-rgw
spec:
  dependsOn:
    - resource:default/cephobjectstore
    - component:default/rook-operator
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	topo := clank.WhirTopology{
		Catalog: cat,
		Resolver: &whir.Resolver{
			BaseURL: srv.URL,
			Client:  http.DefaultClient,
			Queries: map[string]string{
				"cephobjectstore": "objectstore_query",
				"rook-operator":   "rook_query",
			},
		},
	}

	sig := sigBurnAccel()
	sig.OriginService = "ceph-rgw"

	got, err := topo.Topology(context.Background(), sig)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}

	want := proposal.TopologySnapshot{
		Upstream: []proposal.NodeState{
			{Name: "cephobjectstore", State: whir.StateHealthy},
			{Name: "rook-operator", State: whir.StateDegraded},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Topology (-want +got):\n%s", diff)
	}
}

func TestWhirTopology_UncataloguedSubjectYieldsEmptyButAssembleSucceeds(t *testing.T) {
	t.Parallel()

	cat, err := whir.Load([]byte(`
metadata:
  name: ceph-rgw
spec:
  dependsOn:
    - resource:default/cephobjectstore
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	topo := clank.WhirTopology{
		Catalog:  cat,
		Resolver: &whir.Resolver{BaseURL: "http://unused.invalid", Client: http.DefaultClient},
	}

	sig := sigBurnAccel()
	sig.OriginService = "not-in-catalog"

	got, err := topo.Topology(context.Background(), sig)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	if len(got.Upstream) != 0 {
		t.Errorf("Topology(uncatalogued subject).Upstream = %+v, want empty", got.Upstream)
	}

	in := clank.NewIntake(topo, fakeChangeSource())
	sao, err := in.Assemble(context.Background(), sig)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if sao.Version != 1 {
		t.Errorf("Assemble on an uncatalogued subject should still succeed with a v1 SAO, got %+v", sao)
	}
}

// TestCatalogInfo_EveryArmedFaultTargetIsANodeInItsOwnSignalsTopology pins the
// other half of the same join: findNode matches a ChangeEvent.Target against
// topology node names by exact string equality (internal/clank/causal.go:64),
// and flagd shipped as an island in catalog-info.yaml with no dependsOn edge
// in either direction — so cart's and product-catalog's fault events resolved
// to a subject that was in no topology, scored InTopology=false, and were
// excluded from confidence by design (confidence.go:45-48).
func TestCatalogInfo_EveryArmedFaultTargetIsANodeInItsOwnSignalsTopology(t *testing.T) {
	t.Parallel()

	cat, err := whir.LoadCatalogFile(filepath.Join("..", "..", "config", "dev", "whir", "catalog-info.yaml"))
	if err != nil {
		t.Fatalf("load dev catalog: %v", err)
	}

	evCfg, err := evidence.LoadEvidenceConfig(filepath.Join("..", "..", "config", "dev", "whir", "evidence-queries.yaml"))
	if err != nil {
		t.Fatalf("load dev evidence queries: %v", err)
	}

	// Dev chaos scenarios:
	// - cart-failure: signalRef slo_burn:cart (OriginService: cart), fault flagd-config -> target: flagd
	// - product-catalog-failure: signalRef slo_burn:product-catalog (OriginService: product-catalog), fault flagd-config -> target: flagd
	// - acme-api-fault: signalRef slo_burn:acme-api (OriginService: acme-api), fault acme-fault-flag -> target: acme-db
	tests := map[string]struct {
		originService string
		faultCoords   subjects.Coordinates
	}{
		"cart fault target flagd is in cart upstream topology": {
			originService: "cart",
			faultCoords:   subjects.Coordinates{Namespace: "otel-demo", Kind: "ConfigMap", Name: "flagd-config"},
		},
		"product-catalog fault target flagd is in product-catalog upstream topology": {
			originService: "product-catalog",
			faultCoords:   subjects.Coordinates{Namespace: "otel-demo", Kind: "ConfigMap", Name: "flagd-config"},
		},
		"acme fault target acme-db is in acme-api upstream topology": {
			originService: "acme-api",
			faultCoords:   subjects.Coordinates{Namespace: "acme", Kind: "ConfigMap", Name: "acme-fault-flag"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			target := evCfg.Index.For(tc.faultCoords)
			if target == "" {
				t.Fatalf("fault %v has no subject rule", tc.faultCoords)
			}
			edges := cat.Edges(tc.originService)
			if !slices.Contains(edges, target) {
				t.Errorf("origin service %q dependencies %v do not contain fault target %q", tc.originService, edges, target)
			}
		})
	}
}

// TestObservedTopology_FallsBackToTheAuthoredCatalogWhenAServiceEmitsNoTraces pins
// the fallback contract: services emitting no traces fall back to the authored catalog-info.yaml.
func TestObservedTopology_FallsBackToTheAuthoredCatalogWhenAServiceEmitsNoTraces(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		originService string
		wantSnapshot  proposal.TopologySnapshot
	}{
		"FallsBackToTheAuthoredCatalog resolves upstream dependencies from catalog when GraphSource returns no traces": {
			originService: "acme-api",
			wantSnapshot: proposal.TopologySnapshot{
				Upstream: []proposal.NodeState{
					{Name: "acme-db", State: whir.StateHealthy},
				},
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
				case strings.Contains(q, "up{job=\"acme-db\"}"):
					_, _ = w.Write([]byte(`{"data":{"result":[{"value":[0,"1"]}]}}`))
				default:
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
				}
			}))
			defer srv.Close()

			cat := whir.Catalog{
				Entities: []whir.Entity{
					{Name: "acme-api", DependsOn: []string{"acme-db"}},
				},
			}
			resolver := &whir.Resolver{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Queries: map[string]string{
					"acme-db": "up{job=\"acme-db\"}",
				},
			}
			graph := &whir.GraphSource{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Window:  5 * time.Minute,
				Queries: whir.GraphQueries{
					Requests: `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
					Failed:   `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
				},
			}

			topo := clank.ObservedTopology{
				Graph:    graph,
				Fallback: clank.WhirTopology{Catalog: cat, Resolver: resolver},
			}

			sig := sigBurnAccel()
			sig.OriginService = tc.originService

			got, err := topo.Topology(context.Background(), sig)
			if err != nil {
				t.Fatalf("unexpected topology error: %v", err)
			}
			if diff := cmp.Diff(tc.wantSnapshot, got); diff != "" {
				t.Errorf("wrong fallback snapshot (-want +got):\n%s", diff)
			}
		})
	}
}

// TestObservedTopology_PopulatesDownstreamWhichTheAuthoredCatalogCannot pins the
// reverse-dependency traversal: Downstream is populated from reverse catalog edges or observed inbound callers.
func TestObservedTopology_PopulatesDownstreamWhichTheAuthoredCatalogCannot(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		originService string
		wantSnapshot  proposal.TopologySnapshot
	}{
		"PopulatesDownstream resolves calling services into Downstream by inverting catalog edges": {
			originService: "flagd",
			wantSnapshot: proposal.TopologySnapshot{
				Downstream: []proposal.NodeState{
					{Name: "cart", State: whir.StateHealthy},
				},
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
				case strings.Contains(q, "up{job=\"cart\"}"):
					_, _ = w.Write([]byte(`{"data":{"result":[{"value":[0,"1"]}]}}`))
				default:
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
				}
			}))
			defer srv.Close()

			cat := whir.Catalog{
				Entities: []whir.Entity{
					{Name: "cart", DependsOn: []string{"flagd"}},
				},
			}
			resolver := &whir.Resolver{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Queries: map[string]string{
					"cart": "up{job=\"cart\"}",
				},
			}
			graph := &whir.GraphSource{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Window:  5 * time.Minute,
				Queries: whir.GraphQueries{
					Requests:        `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
					Failed:          `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
					InboundRequests: `sum by (client, server) (rate(traces_service_graph_request_total{server="%s"}[%s]))`,
				},
			}

			topo := clank.ObservedTopology{
				Graph:    graph,
				Fallback: clank.WhirTopology{Catalog: cat, Resolver: resolver},
			}

			sig := sigBurnAccel()
			sig.OriginService = tc.originService

			got, err := topo.Topology(context.Background(), sig)
			if err != nil {
				t.Fatalf("unexpected topology error: %v", err)
			}
			if diff := cmp.Diff(tc.wantSnapshot, got); diff != "" {
				t.Errorf("wrong downstream snapshot (-want +got):\n%s", diff)
			}
		})
	}
}

// TestObservedTopology_CarriesEachNodesTrafficShareOntoTheSnapshot pins that
// observed traffic ratios are mapped onto NodeState.TrafficShare.
func TestObservedTopology_CarriesEachNodesTrafficShareOntoTheSnapshot(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		originService string
		wantSnapshot  proposal.TopologySnapshot
	}{
		"CarriesEachNodesTrafficShare populates TrafficShare on Upstream node states from service graph queries": {
			originService: "checkout",
			wantSnapshot: proposal.TopologySnapshot{
				Upstream: []proposal.NodeState{
					{Name: "cart", State: whir.StateHealthy, TrafficShare: 0.75},
					{Name: "payment", State: whir.StateHealthy, TrafficShare: 0.25},
				},
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
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
						{"metric":{"client":"checkout","server":"cart"},"value":[1688745600,"75.0"]},
						{"metric":{"client":"checkout","server":"payment"},"value":[1688745600,"25.0"]}
					]}}`))
				default:
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
				}
			}))
			defer srv.Close()

			graph := &whir.GraphSource{
				BaseURL: srv.URL,
				Client:  http.DefaultClient,
				Window:  5 * time.Minute,
				Queries: whir.GraphQueries{
					Requests: `sum by (client, server) (rate(traces_service_graph_request_total{client="%s"}[%s]))`,
					Failed:   `sum by (client, server) (rate(traces_service_graph_request_failed_total{client="%s"}[%s]))`,
				},
			}

			topo := clank.ObservedTopology{
				Graph:    graph,
				Fallback: clank.WhirTopology{},
			}

			sig := sigBurnAccel()
			sig.OriginService = tc.originService

			got, err := topo.Topology(context.Background(), sig)
			if err != nil {
				t.Fatalf("unexpected topology error: %v", err)
			}
			if diff := cmp.Diff(tc.wantSnapshot, got); diff != "" {
				t.Errorf("wrong traffic share snapshot (-want +got):\n%s", diff)
			}
		})
	}
}

// TestWhirTopology_ReportsATrafficShareTheTopologicalTermCanRead pins that
// topologicalScore reads NodeState.TrafficShare directly when degraded in path.
func TestWhirTopology_ReportsATrafficShareTheTopologicalTermCanRead(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		eventID   string
		target    string
		topo      proposal.TopologySnapshot
		weights   clank.ScoringWeights
		wantScore float64
	}{
		"ReportsATrafficShare passes the non-zero TrafficShare to the topological score for degraded in-path nodes": {
			eventID: "deploy:cart-v2",
			target:  "cart",
			topo: proposal.TopologySnapshot{
				Upstream: []proposal.NodeState{
					{Name: "cart", State: "degraded", TrafficShare: 0.8},
				},
			},
			weights: clank.ScoringWeights{
				RecencyHalfLife:       30 * time.Minute,
				HistoricalHalfLife:    30 * time.Minute,
				HistoricalAloneCap:    0.5,
				NegativeSignalPenalty: 0.2,
				Temporal:              0.333,
				Topological:           0.334,
				Historical:            0.333,
				CaseBaseBaseline:      0.9,
			},
			wantScore: 0.8,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scorer := clank.NewCausalScorer()
			change := proposal.ChangeSnapshot{
				Events: []proposal.ChangeEvent{
					{ID: tc.eventID, Target: tc.target, Age: 0},
				},
			}

			scores := scorer.Score("slo_burn:checkout", change, tc.topo, tc.weights)
			if len(scores) != 1 {
				t.Fatalf("want 1 score, got %d", len(scores))
			}
			if diff := cmp.Diff(tc.wantScore, scores[0].Topological); diff != "" {
				t.Errorf("wrong topological score (-want +got):\n%s", diff)
			}
		})
	}
}
