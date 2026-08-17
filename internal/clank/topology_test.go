package clank_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

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
