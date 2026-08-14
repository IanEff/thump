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

	"go.opentelemetry.io/otel/trace"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/publish/publishtest"
	"github.com/ianeff/thump/internal/rattle"
	"github.com/ianeff/thump/internal/tracing"
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
	if !strings.Contains(out.String(), "PROM_URL") {
		t.Error("the startup failure record should name the missing var", out.String())
	}
}

// TestThumpTestWatch_MatchesTheAuthoredContract is the drift guard: the
// checked-in config/thump-test/rattle/watch.yaml must still declare exactly
// the SLO contract authored for it — Ceph, the OTel demo, and acme, three
// domains sharing one watch list but no signal, failure class, or catalog
// action with each other. If this goes red after hand-editing the YAML,
// that's the guard working.
func TestThumpTestWatch_MatchesTheAuthoredContract(t *testing.T) {
	got, err := rattle.LoadWatch("../../config/thump-test/rattle/watch.yaml")
	if err != nil {
		t.Fatalf("LoadWatch: %v", err)
	}
	want := []rattle.SLO{
		{
			ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-rgw-availability:v1",
			Dependencies: []rattle.Dependency{{Name: "cephobjectstore", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "ceph-rgw-saturation", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "ceph-rgw-saturation:v1",
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
		{
			ID: "ceph-redundancy", Object: "cephblockpool", Tier: "tier-1", Objective: 0.999,
			ContractRef:  "ceph-redundancy:v1",
			Dependencies: []rattle.Dependency{{Name: "cephcluster", Role: "blocking"}, {Name: "rook-operator", Role: "blocking"}},
		},
		{
			ID: "product-catalog-availability", Object: "product-catalog", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "product-catalog-availability:v1",
			Dependencies: []rattle.Dependency{{Name: "frontend", Role: "blocking"}, {Name: "flagd", Role: "blocking"}},
		},
		{
			ID: "cart-availability", Object: "cart", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "cart-availability:v1",
			Dependencies: []rattle.Dependency{{Name: "checkout", Role: "blocking"}, {Name: "flagd", Role: "blocking"}},
		},
		{
			ID: "acme-api-availability", Object: "acme-api", Tier: "tier-1", Objective: 0.99,
			ContractRef:  "acme-api-availability:v1",
			Dependencies: []rattle.Dependency{{Name: "acme-db", Role: "blocking"}, {Name: "acme-cache", Role: "optional"}},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("config/thump-test/rattle/watch.yaml drifted from the authored contract (-want +got):\n%s", diff)
	}
}

func TestThumpTestWatch_EverySLODeclaresDependencies(t *testing.T) {
	got, err := rattle.LoadWatch("../../config/thump-test/rattle/watch.yaml")
	if err != nil {
		t.Fatalf("LoadWatch: %v", err)
	}
	for _, slo := range got {
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
		{Name: "rook-operator", State: "healthy"},
		{Name: "cephobjectstore", State: "unknown"}, // no Queries entry -> unknown, not dropped
	}
	if diff := cmp.Diff(want, got.Topology.Upstream); diff != "" {
		t.Errorf("Topology.Upstream (-want +got):\n%s", diff)
	}
}

func TestRunLoop_DeliversWhatItLogs(t *testing.T) {
	slo := rattle.SLO{ID: "ceph-osd-latency"}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)}) // fires once
	pub := &publishtest.CapturePublisher[signal.Detection]{}
	rattle.RunLoopForTest(onceCtx(), r, discardLogger(), pub)
	if len(pub.Delivered) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(pub.Delivered))
	}
}

// traceCapturePublisher records the ctx a Publish call arrived with, not just
// the object — publishtest.CapturePublisher only keeps the latter, and B1
// needs the former: proof that runLoop minted the incident's root span
// before ever calling Publish, not after.
type traceCapturePublisher struct {
	ctx       context.Context
	delivered signal.Detection
}

func (c *traceCapturePublisher) Publish(ctx context.Context, _ string, d signal.Detection) error {
	c.ctx = ctx
	c.delivered = d
	return nil
}

// TestRunLoop_PublishesWithTraceContextKeyedByFingerprint pins the one
// bespoke line B1 needs from rattle: it's the only beat that mints a trace's
// root — every downstream beat just extracts what's already on the wire (see
// internal/broker/subscriber_test.go). The expected trace_id is derived from
// whatever fingerprint the detection actually got, not a hardcoded literal,
// so this test can't accidentally pass by coincidence.
func TestRunLoop_PublishesWithTraceContextKeyedByFingerprint(t *testing.T) {
	slo := rattle.SLO{ID: "ceph-osd-latency"}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)}) // fires once
	pub := &traceCapturePublisher{}
	rattle.RunLoopForTest(onceCtx(), r, discardLogger(), pub)

	if pub.ctx == nil {
		t.Fatal("Publish was never called")
	}
	want := tracing.TraceIDFromFingerprint(pub.delivered.Fingerprint)
	got := trace.SpanContextFromContext(pub.ctx).TraceID()
	if got != want {
		t.Errorf("Publish's ctx carries trace_id %s, want %s (tracing.TraceIDFromFingerprint(%q)) — runLoop must mint the root span from the detection's fingerprint before publishing",
			got, want, pub.delivered.Fingerprint)
	}
}

func TestNewReconciler_WiresTheContractSoConfidenceIsLive(t *testing.T) {
	slo := rattle.SLO{ID: "ceph-rgw-availability"}
	r := rattle.NewReconcilerForTest("http://unused", nil, nil, nil, nil)
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
	r := rattle.NewReconcilerForTest("http://unused", nil, nil, nil, nil)
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

func TestMain_ReturnsNonZeroWhenRequiredConfigIsMissing(t *testing.T) {
	t.Setenv("PROM_URL", "")
	t.Setenv("RATTLE_WATCH", "")

	var stdout, stderr bytes.Buffer
	code := rattle.Main(nil, &stdout, &stderr, "dev", "none", "unknown")

	if code != 1 {
		t.Errorf("want exit code 1 for missing config, got %d", code)
	}
	if stdout.Len() == 0 {
		t.Error("want a startup failure record on stdout, got none")
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func onceCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
