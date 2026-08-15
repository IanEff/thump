package clank_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/subjects"
	"github.com/ianeff/thump/internal/whir"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestMain_VersionFlag(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	args := []string{"-version"}
	exitCode := clank.Main(args, stdout, stderr, "v1.0.0", "abcdef", "2026-06-29")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	want := "clank v1.0.0\ncommit: abcdef\nbuilt: 2026-06-29\n"
	if diff := cmp.Diff(want, stdout.String()); diff != "" {
		t.Error("wrong --version output (-want +got)\n", diff)
	}
}

func TestMain_MissingInboxReturnsOne(t *testing.T) {
	t.Setenv("CLANK_INBOX", "") // hermetic — don't inherit the shell's
	t.Setenv("CLANK_OUTBOX", t.TempDir())
	t.Setenv("CLANK_OUTCOMES", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var out, errb bytes.Buffer
	code := clank.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing CLANK_INBOX should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "CLANK_INBOX") {
		t.Error("stderr should name the missing var:", errb.String())
	}
}

func TestMain_MissingOutboxReturnsOne(t *testing.T) {
	t.Setenv("CLANK_INBOX", t.TempDir())
	t.Setenv("CLANK_OUTBOX", "")
	t.Setenv("CLANK_OUTCOMES", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var out, errb bytes.Buffer
	code := clank.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing CLANK_OUTBOX should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "CLANK_OUTBOX") {
		t.Error("stderr should name the missing var:", errb.String())
	}
}

func TestMain_MissingOutcomesReturnsOne(t *testing.T) {
	t.Setenv("CLANK_INBOX", t.TempDir())
	t.Setenv("CLANK_OUTBOX", t.TempDir())
	t.Setenv("CLANK_OUTCOMES", "")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var out, errb bytes.Buffer
	code := clank.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing CLANK_OUTCOMES should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "CLANK_OUTCOMES") {
		t.Error("stderr should name the missing var:", errb.String())
	}
}

func TestMain_MissingAPIKeyReturnsOne(t *testing.T) {
	t.Setenv("CLANK_INBOX", t.TempDir())
	t.Setenv("CLANK_OUTBOX", t.TempDir())
	t.Setenv("CLANK_OUTCOMES", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	var out, errb bytes.Buffer
	code := clank.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing ANTHROPIC_API_KEY should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "ANTHROPIC_API_KEY") {
		t.Error("stderr should name the missing var:", errb.String())
	}
}

func TestMain_TheEngineAndReturnEdgeShareOneLedgerAndCaseBase(t *testing.T) {
	t.Parallel()
	// build the loop the way Main does, then prove the two halves are wired to
	// the SAME state — a full round trip: propose (records a set) → hand-built
	// Outcome for that set → ReturnEdge observes it → the case is banked where
	// the scorer will find it next cycle.
	loop := newTestLoop(t) // constructs Engine + ReturnEdge via the SAME shared() helper Main uses

	det := seamDetection(t)
	set, err := loop.Engine.Propose(context.Background(), det) // records into the shared ledger
	if err != nil {
		t.Fatal(err)
	}

	// an Outcome answering that exact set, dropped in the return-edge inbox:
	writeOutcomeFor(t, loop.OutcomeInbox, set) // live success, DecisionRef/SignalRef threaded
	if err := loop.ReturnEdge.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}

	// the outcome was MATCHED (not unmatched) — proof the ledgers are one:
	if n := unmatchedCount(t, loop.OutcomeInbox); n != 0 {
		t.Fatalf("the return edge saw an empty ledger — Main built TWO *MemProposalLog: %d unmatched", n)
	}
	// …and the case is where the scorer reads it — proof the case bases are one:
	if got := len(loop.Cases.Cases(det.Fingerprint)); got != 1 {
		t.Errorf("Absorb banked into a case base the scorer can't see: want 1, got %d", got)
	}
}

func TestLoop_DeliversNonZeroConfidenceThroughProductionWiring(t *testing.T) {
	t.Parallel()

	// Every test engine sets Weights by hand, so only a run through the
	// constructors Main actually calls can prove the tuning ships. The
	// scripted run is well-grounded (one live citation, catalogued action,
	// self-reported 0.87) — if that emits 0, the wiring dropped the weights.
	loop := newTestLoop(t)

	set, err := loop.Engine.Propose(context.Background(), seamDetection(t))
	if err != nil {
		t.Fatal(err)
	}

	if set.Proposals[0].Confidence <= 0 {
		t.Errorf("production wiring emits zero confidence for a grounded candidate:\n%+v", set.Proposals[0])
	}
}

func TestMain_ReturnsNonZeroWhenRequiredConfigIsMissing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ACTION_CATALOG", "")
	t.Setenv("FAILURE_CLASSES", "")

	var stdout, stderr bytes.Buffer
	code := clank.Main(nil, &stdout, &stderr, "dev", "none", "unknown")

	if code != 1 {
		t.Errorf("want exit code 1 for missing config, got %d", code)
	}
	if stderr.Len() == 0 {
		t.Error("want error message printed to stderr, got none")
	}
}

func TestBuildIntake_WarnsOnEverySilentFallback(t *testing.T) {
	tests := map[string]struct {
		cfg      config.Clank
		wantMsgs []string
	}{
		"buildIntake warns for both the change source and the topology source when whir is unconfigured": {
			cfg: config.Clank{},
			wantMsgs: []string{
				"no change source configured — causal scoring is inert",
				"no topology source configured — clank reasoning without a blast-radius map",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// captureLog mutates the process-wide default logger — no t.Parallel().
			getLines := captureLog(t)

			if _, err := clank.BuildIntakeForTest(tc.cfg, nil, nil, nil, nil, 2*time.Hour); err != nil {
				t.Fatal(err)
			}

			got := warnMessages(getLines())
			for _, want := range tc.wantMsgs {
				if !slices.Contains(got, want) {
					t.Errorf("want a WARN %q, got %v", want, got)
				}
			}
		})
	}
}

// warnMessages pulls the msg field out of every captured WARN-level line —
// captureLog's handler is set at LevelInfo, so INFO lines are in the mix too.
func warnMessages(lines []map[string]any) []string {
	var msgs []string
	for _, l := range lines {
		if l["level"] == "WARN" {
			msgs = append(msgs, l["msg"].(string))
		}
	}
	return msgs
}

// testLoop mirrors clank's unexported `loop` type field-for-field. We can't
// name that type here (it's unexported, in package clank) — but newTestLoop
// CAN hold a value of it via `:=` (Go lets you hold what you can't name), and
// copy its exported fields out into this same-package, fully nameable shape.
type testLoop struct {
	Engine       *clank.Engine
	ReturnEdge   *clank.ReturnEdge
	Cases        *clank.CaseBase
	OutcomeInbox string
}

// newTestLoop builds a loop through clank.NewLoopForTest — the SAME newLoop
// Main calls — so this test exercises the real construction path, not a
// hand-rolled copy of it. Only the Model/Tools/Intake/Catalog inputs are
// test fakes; the wiring itself is production code.
func newTestLoop(t *testing.T) testLoop {
	t.Helper()
	model := &fakeModel{script: []reason.Completion{
		// turn 1: gather live evidence — required for the gate's evidence floor.
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		// turn 2: propose a catalogued action.
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{`{"q":"burn"}`}}},
		})}}},
	}}
	tools := map[string]reason.Tool{"metrics": metricsTool{}}
	intake := clank.NewIntake(
		fakeTopo{snap: proposal.TopologySnapshot{
			Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
		}},
		fakeChange{snap: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
			{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
		}}},
	)
	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
	}})
	store := clank.NewMemStore()

	l := clank.NewLoopForTest(model, tools, intake, cat, t.TempDir(), t.TempDir(), store)
	return testLoop{Engine: l.Engine, ReturnEdge: l.ReturnEdge, Cases: l.Cases, OutcomeInbox: l.OutcomeInbox}
}

// writeOutcomeFor drops a live-success Outcome answering the given set into
// dir, threading the SignalRef and a candidate's ContractRef through — the
// fields ReturnEdge.Tick / MemProposalLog.Observe actually match on.
func writeOutcomeFor(t *testing.T, dir string, set proposal.Set) {
	t.Helper()
	o := outcome.Outcome{
		ID:          "out:" + set.SignalRef + ":1000",
		DecisionRef: "dec:" + set.SignalRef,
		SignalRef:   set.SignalRef,
		ContractRef: set.Proposals[0].ContractRef,
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultSuccess,
		ExecutedAt:  time.Unix(1000, 0),
	}
	writeOutcomeYAML(t, dir, "outcome.yaml", o)
}

// unmatchedCount counts outcomes ReturnEdge.Tick couldn't match to an open
// set — the trap's tell: if the engine and return edge don't share a ledger,
// every outcome lands here instead of being absorbed.
func unmatchedCount(t *testing.T, inbox string) int {
	t.Helper()
	return yamlCount(t, filepath.Join(inbox, "unmatched"))
}

// TestBuildTools_FullyConfiguredReachesSubjectAwareEvidenceTools is the
// wiring pin the loki and kube tools went without. A tool holding an empty
// SubjectIndex satisfies the Tool interface, returns Live refs, logs
// identically, and stamps no Subject — so gate.go's coherentSubject fails
// closed on every one of its citations and it can neither ground a proposal
// nor count as a second backend. The suite stays green the whole time,
// because nothing else asks whether the composition root handed it any rules.
func TestBuildTools_FullyConfiguredReachesSubjectAwareEvidenceTools(t *testing.T) {
	t.Parallel()

	ev := evidence.Config{
		Queries: map[string]evidence.Query{"ceph_health": {Query: "ceph_health_status", Subject: "ceph-cluster"}},
		Index:   subjects.SubjectIndex{{Subject: "ceph-osd", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}}}},
	}
	cfg := config.Clank{
		PromURL:         "http://prom:9090",
		EvidenceQueries: "/etc/evidence-queries.yaml",
		LokiURL:         "http://loki:3100",
	}

	tools := clank.BuildToolsForTest(cfg, nil, ev, kubefake.NewSimpleClientset())

	metrics, ok := tools["metrics"].(*evidence.MetricsTool)
	if !ok {
		t.Fatalf("fully-configured buildTools must reach a real MetricsTool, got %T", tools["metrics"])
	}
	if diff := cmp.Diff(ev.Queries, metrics.Queries); diff != "" {
		t.Error("the metrics tool must reach the per-query subject tags (-want +got)\n", diff)
	}

	loki, ok := tools["loki"].(*evidence.LokiTool)
	if !ok {
		t.Fatalf("fully-configured buildTools must reach a real LokiTool, got %T", tools["loki"])
	}
	if diff := cmp.Diff(ev.Index, loki.Subjects); diff != "" {
		t.Error("the loki tool must reach the subject rules, not an empty index (-want +got)\n", diff)
	}

	kube, ok := tools["kube"].(*evidence.KubeTool)
	if !ok {
		t.Fatalf("buildTools with an in-cluster client must reach a real KubeTool, got %T", tools["kube"])
	}
	if diff := cmp.Diff(ev.Index, kube.Subjects); diff != "" {
		t.Error("the kube tool must reach the subject rules, not an empty index (-want +got)\n", diff)
	}
}

// TestBuildTools_RegistersNoKubeToolWithoutAnInClusterClient pins the other
// half of the same wiring question. Offline — the dir-poll path, and every
// test run — there is no cluster to query, and a kube tool built around a
// nil client answers every citation with a panic rather than an absence.
func TestBuildTools_RegistersNoKubeToolWithoutAnInClusterClient(t *testing.T) {
	t.Parallel()

	tools := clank.BuildToolsForTest(config.Clank{LokiURL: "http://loki:3100"}, nil, evidence.Config{}, nil)

	if got, ok := tools["kube"]; ok {
		t.Errorf("buildTools with no in-cluster client must register no kube tool, got %T", got)
	}
}

// TestClientsFor_YieldsATrulyNilInterfaceWhenAClientCannotBeBuilt is the
// guard the nil check above is worthless without. Both constructors return a
// concrete pointer, and a nil *Clientset assigned to a kubernetes.Interface
// makes an interface that is not nil — so a caller that degrades on nil would
// instead wire a tool around a client that panics on first use. This fails
// the moment either return path is written through the concrete type.
func TestClientsFor_YieldsATrulyNilInterfaceWhenAClientCannotBeBuilt(t *testing.T) {
	t.Parallel()

	kube, argo := clank.ClientsForTest(&rest.Config{Host: "://not-a-url"})

	if kube != nil {
		t.Errorf("a kube client that failed to build must come back as a nil interface, got %T", kube)
	}
	if argo != nil {
		t.Errorf("a dynamic client that failed to build must come back as a nil interface, got %T", argo)
	}
}

// TestShippedEvidenceConfigs_NameRealTopologyNodes is the vocabulary guard
// TestArgoChangeSource_TargetsShareTheTopologyCatalogsVocabulary applies to
// the change source, applied here to the subject join. EvidenceQuery.Subject,
// SubjectRule.Subject and NodeState.Name are all strings, so a tag naming a
// node the graph never declares parses, resolves, stamps an EvidenceRef the
// gate then silently refuses, and leaves the suite green — a citation that
// can never ground anything, indistinguishable from one that simply didn't.
func TestShippedEvidenceConfigs_NameRealTopologyNodes(t *testing.T) {
	t.Parallel()

	for _, rig := range []string{"dev", "thump-test"} {
		t.Run(rig, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join("..", "..", "config", rig, "whir")

			cat, err := whir.LoadCatalogFile(filepath.Join(dir, "catalog-info.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			nodes := make(map[string]bool, len(cat.Entities))
			for _, e := range cat.Entities {
				nodes[e.Name] = true
			}

			ev, err := evidence.LoadEvidenceConfig(filepath.Join(dir, "evidence-queries.yaml"))
			if err != nil {
				t.Fatal(err)
			}

			for name, q := range ev.Queries {
				if q.Subject != "" && !nodes[q.Subject] {
					t.Errorf("query %q is tagged subject: %q, which names no entity in this rig's catalog-info.yaml", name, q.Subject)
				}
			}
			for _, rule := range ev.Index {
				if !nodes[rule.Subject] {
					t.Errorf("subject rule for namespace %q names %q, which is no entity in this rig's catalog-info.yaml",
						rule.Namespace, rule.Subject)
				}
			}
		})
	}
}

func TestBuildIntake_FullyConfiguredReachesRealTopology(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.yaml")
	queriesPath := filepath.Join(dir, "queries.yaml")
	for _, f := range []string{catalogPath, queriesPath} {
		if err := os.WriteFile(f, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Clank{PromURL: "http://prom:9090", WhirCatalog: catalogPath, WhirStateQueries: queriesPath}
	intake, err := clank.BuildIntakeForTest(cfg, nil, nil, nil, nil, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	got := clank.IntakeTopologyForTest(intake)
	if _, ok := got.(clank.WhirTopology); !ok {
		t.Errorf("fully-configured buildIntake must reach a real WhirTopology, got %T", got)
	}
}

// TestBuildIntake_ComposesEveryConfiguredChangeSource verifies that buildIntake
// wires both KubeChangeSource and ArgoChangeSource into the fan-out change
// source when both are configured, and that Changes concatenates their events.
func TestBuildIntake_ComposesEveryConfiguredChangeSource(t *testing.T) {
	t.Parallel()

	// buildIntake wires KubeChangeSource and ArgoChangeSource with no way to
	// inject a fake clock, so fixture ages must anchor to the real clock the
	// sources will call — a hardcoded "now" only stays inside the lookback
	// window during part of the real day and makes the test flaky.
	now := time.Now().UTC()
	subjects := subjects.SubjectIndex{
		{Subject: "cart", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Kind: "Deployment", Name: "cart"}},
		{Subject: "cephblockpool", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Kind: "CephBlockPool", Name: "replicapool"}},
	}

	kubeFake := kubefake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "otel-demo", Name: "cart", Generation: 2},
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, LastUpdateTime: metav1.Time{Time: now.Add(-10 * time.Minute)}},
			},
		},
	})

	app := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "rook-storage", "namespace": "argocd"},
		"status": map[string]any{
			"operationState": map[string]any{
				"phase":      "Succeeded",
				"finishedAt": now.Add(-20 * time.Minute).Format(time.RFC3339),
				"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
			},
			"resources": []any{
				map[string]any{"kind": "CephBlockPool", "namespace": "rook-ceph", "name": "replicapool"},
			},
		},
	}}
	argoFake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		}, app)

	cfg := config.Clank{ArgoEnabled: true}
	intake, err := clank.BuildIntakeForTest(cfg, nil, kubeFake, argoFake, subjects, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	changeSource := clank.IntakeChangeForTest(intake)
	snap, err := changeSource.Changes(t.Context(), signal.Detection{})
	if err != nil {
		t.Fatalf("unexpected error from composed change source: %v", err)
	}

	var targets []string
	for _, e := range snap.Events {
		targets = append(targets, e.Target)
	}
	slices.Sort(targets)
	wantTargets := []string{"cart", "cephblockpool"}
	if diff := cmp.Diff(wantTargets, targets); diff != "" {
		t.Error("composed change source must include events from both Kube and Argo sources (-want +got)\n", diff)
	}
}

// TestBuildIntake_WithNoConfiguredChangeSourceStillBehavesLikeTheNoop verifies
// that an unconfigured Intake returns an empty change snapshot without error.
func TestBuildIntake_WithNoConfiguredChangeSourceStillBehavesLikeTheNoop(t *testing.T) {
	t.Parallel()

	cfg := config.Clank{}
	intake, err := clank.BuildIntakeForTest(cfg, nil, nil, nil, nil, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	changeSource := clank.IntakeChangeForTest(intake)
	snap, err := changeSource.Changes(t.Context(), signal.Detection{})
	if err != nil {
		t.Fatalf("unexpected error from unconfigured change source: %v", err)
	}
	if len(snap.Events) != 0 {
		t.Errorf("unconfigured change source must return 0 events, got %d", len(snap.Events))
	}
}

// TestShippedEvidenceConfigs_CarryRulesAChangedResourceCanMatch is the guard the
// no-op pins above cannot provide. Reaching a real ArgoChangeSource proves the
// seam is filled; it says nothing about whether the names it emits mean anything
// to the graph they are joined against.
//
// A changed Kubernetes object states a namespace, kind and name — never labels,
// which ArgoCD's resource inventory does not publish. So a rig whose rules are
// all label-constrained resolves every evidence query and no change event at
// all: the source runs, the events carry Kubernetes names the catalog never
// declares, every score lands out of topology, and the suite stays green. That
// is the shape that shipped, and one rule per rig that a resource can actually
// match is what refutes it.
func TestShippedEvidenceConfigs_CarryRulesAChangedResourceCanMatch(t *testing.T) {
	t.Parallel()

	for _, rig := range []string{"dev", "thump-test"} {
		t.Run(rig, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join("..", "..", "config", rig, "whir")

			ev, err := evidence.LoadEvidenceConfig(filepath.Join(dir, "evidence-queries.yaml"))
			if err != nil {
				t.Fatal(err)
			}

			var matchable int
			for _, rule := range ev.Index {
				if len(rule.Labels) == 0 {
					matchable++
				}
			}
			if matchable == 0 {
				t.Error("every subject rule on this rig constrains labels, which an ArgoCD resource entry never carries — no change event can resolve into the topology, so the causal term is inert here")
			}
		})
	}
}

// TestArgoChangeSource_ResolvesTheShippedRigsOwnCoordinates joins the two halves
// the guard above only checks separately: the rig's authored rules, and the
// coordinates its ArgoCD actually reports. The Ceph resources are the case that
// matters, because that is where the two vocabularies genuinely diverge — the
// CephBlockPool is named "replicapool" and the topology node is named
// "cephblockpool", so a fixture that picks names which happen to coincide (as
// the OTel demo's do) proves nothing.
func TestArgoChangeSource_ResolvesTheShippedRigsOwnCoordinates(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "..", "config", "thump-test", "whir")
	cat, err := whir.LoadCatalogFile(filepath.Join(dir, "catalog-info.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[string]bool, len(cat.Entities))
	for _, e := range cat.Entities {
		nodes[e.Name] = true
	}
	ev, err := evidence.LoadEvidenceConfig(filepath.Join(dir, "evidence-queries.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// The inventory a real rook-storage sync reports on this rig.
	app := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata":   map[string]any{"name": "rook-storage", "namespace": "argocd"},
		"status": map[string]any{
			"operationState": map[string]any{
				"phase":      "Succeeded",
				"finishedAt": "2026-07-30T11:55:00Z",
				"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
			},
			"resources": []any{
				map[string]any{"kind": "CephBlockPool", "namespace": "rook-ceph", "name": "replicapool"},
				map[string]any{"kind": "Deployment", "namespace": "rook-ceph", "name": "rook-ceph-operator"},
			},
		},
	}}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}: "ApplicationList",
		}, app)

	src := evidence.ArgoChangeSource{Client: fake, Subjects: ev.Index, Now: func() time.Time {
		return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	}}
	snap, err := src.Changes(t.Context(), signal.Detection{})
	if err != nil {
		t.Fatal(err)
	}

	var resolved []string
	for _, e := range snap.Events {
		if nodes[e.Target] {
			resolved = append(resolved, e.Target)
		}
	}
	if diff := cmp.Diff([]string{"cephblockpool", "rook-operator"}, resolved); diff != "" {
		t.Error("the shipped rules must resolve this rig's own synced resources onto catalog node names (-want +got)\n", diff)
	}
}
