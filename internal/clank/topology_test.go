package clank_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
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
