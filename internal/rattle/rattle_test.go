package rattle_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/rattle"
	"github.com/ianeff/thump/internal/whir"
)

func TestMain_PrintsVersionAndReturnsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := rattle.Main([]string{"-version"}, &out, &errb, "1.2.3", "abc123", "2026-07-01")
	if code != 0 {
		t.Errorf("version should exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "rattle 1.2.3") {
		t.Error("version output mmissing the version", cmp.Diff("rattle 1.2.3", out.String()))
	}
}

func TestMain_MissingPromURLReturnsOne(t *testing.T) {
	t.Setenv("PROM_URL", "") // hermetic — don't inherit the shell's
	var out, errb bytes.Buffer
	code := rattle.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing PROM_URL should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "PROM_URL") {
		t.Error("stderr should name the missing var", errb.String())
	}
}

func TestLoadSLOs_DeclaresTheLabContract(t *testing.T) {
	want := []rattle.SLO{
		{
			ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-rgw-availability:v1",
			Dependencies: []rattle.Dependency{{Name: "cephobjectstore", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "ceph-osd-latency", Object: "ceph-osd", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "ceph-osd-latency:v1",
			Dependencies: []rattle.Dependency{{Name: "cephblockpool", Role: "blocking"}, {Name: "ceph-node-1", Role: "blocking"}, {Name: "ceph-node-2", Role: "blocking"}, {Name: "ceph-node-3", Role: "blocking"}},
		},
		{
			ID: "ceph-health", Object: "ceph-cluster", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-health:v1",
			Dependencies: []rattle.Dependency{{Name: "cephcluster", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "argocd-sync", Object: "argocd", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "argocd-sync:v1",
			Dependencies: []rattle.Dependency{{Name: "cilium", Role: "blocking"}, {Name: "rook-operator", Role: "optional"}},
		},
	}
	if diff := cmp.Diff(want, rattle.LoadSLOsForTest()); diff != "" {
		t.Errorf("watch list drifted from the lab contract (-want +got):\n%s", diff)
	}
}

func TestLoadSLOs_EverySLODeclaresDependencies(t *testing.T) {
	for _, slo := range rattle.LoadSLOsForTest() {
		if len(slo.Dependencies) == 0 {
			t.Errorf("%s declares no dependencies — EnrichTopology will silently no-op for it", slo.ID)
		}
	}
}

func TestWhirTopologySource_EnrichesWithUnknownVisible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("query") {
		case "up{job=\"rook-operator\"}":
			_, _ = fmt.Fprint(w, `{"data":{"result":[{"value":[0,"1"]}]}}`) // healthy
		default:
			http.Error(w, "boom", http.StatusInternalServerError) // -> unknown
		}
	}))
	defer srv.Close()

	src := &rattle.WhirTopologySource{Resolver: &whir.Resolver{
		BaseURL: srv.URL,
		Queries: map[string]string{"rook-operator": `up{job="rook-operator"}`}, // "cephobjectstore" deliberately absent
	}}

	slo := rattle.SLO{Dependencies: []rattle.Dependency{
		{Name: "rook-operator", Role: "blocking"},
		{Name: "cephobjectstore", Role: "blocking"},
	}}

	got := rattle.EnrichTopology(context.Background(), signal.Detection{}, slo, src)

	want := []signal.ObservedNode{
		{Service: "rook-operator", State: "healthy"},
		{Service: "cephobjectstore", State: "unknown"}, // no Queries entry -> unknown, not dropped
	}
	if diff := cmp.Diff(want, got.Topology.Upstream); diff != "" {
		t.Errorf("Topology.Upstream (-want +got):\n%s", diff)
	}
}

func TestRunLoop_DeliversWhatItLogs(t *testing.T) {
	slo := rattle.SLO{ID: "ceph-osd-latency"}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)}) // fires once
	pub := &publisher{}
	rattle.RunLoopForTest(onceCtx(), r, discardLogger(), pub)
	if len(pub.delivered) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(pub.delivered))
	}
}

func TestNewReconciler_WiresTheContractSoConfidenceIsLive(t *testing.T) {
	slo := rattle.SLO{ID: "ceph-rgw-availability"}
	r := rattle.NewReconcilerForTest("http://unused", nil, nil)
	r.SLOs = []rattle.SLO{slo}
	r.Source = fakeSource{slo.ID: freshWindow(1, 2, 4, 8)} // recent timestamps, fires on acceleration

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 detection, got %d", len(got))
	}
	if diff := cmp.Diff(1.0, got[0].Divergence.Confidence); diff != "" {
		t.Error("Main's Reconciler must carry a live Contract — confidence should read 1.0, not the pre-wiring zero-value", diff)
	}
}

func TestNewReconciler_WiresTheContractSoStaleWindowsAreSkipped(t *testing.T) {
	slo := rattle.SLO{ID: "ceph-rgw-availability"}
	r := rattle.NewReconcilerForTest("http://unused", nil, nil)
	r.SLOs = []rattle.SLO{slo}
	r.Source = fakeSource{slo.ID: window(1, 2, 4, 8)} // epoch-anchored — ancient by wall-clock

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Error("Main wires a 5-minute freshness bound — a stale window must not fire, even though the detector would", cmp.Diff(0, len(got)))
	}
}

func freshWindow(rates ...float64) []rattle.Sample {
	out := make([]rattle.Sample, len(rates))
	base := time.Now().Add(-time.Duration(len(rates)) * time.Minute)
	for i, r := range rates {
		out[i] = rattle.Sample{T: base.Add(time.Duration(i) * time.Minute), BurnRate: r}
	}
	return out
}

type publisher struct {
	delivered []signal.Detection
}

func (p *publisher) Publish(_ context.Context, _ string, d signal.Detection) error {
	p.delivered = append(p.delivered, d)
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func onceCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
