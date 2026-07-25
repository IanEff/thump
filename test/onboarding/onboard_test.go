// Package onboarding_test plays the operator: it onboards a domain the
// engine has never heard of using nothing but authored YAML, and drives that
// domain through every beat. The fixture service is deliberately `acme` —
// never ceph, otel, flagd, or cart — so the day onboarding needs a
// domain-specific Go discriminator, this test is where it shows up.
package onboarding_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/configtest"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/publish/publishtest"
	"github.com/ianeff/thump/internal/rattle"
	"github.com/ianeff/thump/internal/thump"
	"github.com/ianeff/thump/internal/whir"
)

// acmeDir is the only place this test names a path — every domain fact below
// it is read from a file, never written in Go.
func acmeDir(parts ...string) string {
	return filepath.Join(append([]string{"testdata", "acme"}, parts...)...)
}

// fakeProm serves a Prometheus-shaped instant-query response for any query,
// so the authored state and evidence queries are actually issued and parsed
// rather than merely loaded. The value is positive, which whir reads as
// healthy and the metrics tool cites as live.
func fakeProm(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "" {
			http.Error(w, "no query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1750000000,"0.42"]}]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// scriptedModel replays a fixed Completion sequence, so the loop is driven
// deterministically and with no API key. It stands in for the provider, not
// for any part of the engine under test.
type scriptedModel struct {
	script []clank.Completion
	i      int
}

func (m *scriptedModel) Complete(_ context.Context, _ []clank.Message, _ []clank.ToolSpec) (clank.Completion, error) {
	if m.i >= len(m.script) {
		return clank.Completion{}, nil // out of script: no tool calls, loop ends
	}
	c := m.script[m.i]
	m.i++
	return c, nil
}

// noChange is production's ChangeSource: clank wires a noop until a real
// deploy-event feed exists, so the SAO acme reasons over carries no change
// events — the same shape a live run assembles.
type noChange struct{}

func (noChange) Changes(_ context.Context, _ signal.Detection) (proposal.ChangeSnapshot, error) {
	return proposal.ChangeSnapshot{}, nil
}

// acmeSignal is what rattle emits for the authored SLO: a divergence on
// acme-api with rattle's own trust in the reading. clank never recomputes any
// of this — the fingerprint and confidence are trusted read-only input.
func acmeSignal() signal.Detection {
	return signal.Detection{
		Name:          "acme-api-availability-burn-001",
		Fingerprint:   "fp-acme-api-availability-001",
		OriginService: "acme-api",
		ServiceTier:   "tier-1",
		DetectorType:  "burn_rate_acceleration",
		Divergence: signal.Divergence{
			Metric: "acme_api_error_ratio", Observed: 0.42, Baseline: 0.001,
			Confidence: 0.9, Trajectory: "accelerating",
		},
		Impact: signal.Impact{
			Severity:    signal.Severity{DegradationPct: 0.42, Trajectory: "accelerating"},
			BlastRadius: signal.BlastRadius{AffectedPct: 0.6, Velocity: "fast", DownstreamConsumers: 2},
		},
		DetectedAt: time.Now(),
	}
}

// proposeArgs marshals the Set the scripted model hands back through the
// propose tool — the same JSON a real model emits.
func proposeArgs(t *testing.T, ps proposal.Set) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}
	return b
}

// TestOperator_OnboardsANewDomainInConfigAlone is Phase O's acceptance claim:
// seven authored YAML files are enough to make a new domain detectable,
// reasoned about, governable, and executable. Nothing below constructs a
// domain fact in Go — every action, class, threshold, topology edge, and
// query comes off disk through the loader production uses.
//
// The one thing it does build in Go is the composition root, because that
// lives in each beat's unexported Main; the claim under test is that the
// config is sufficient, not that the wiring is shared.
func TestOperator_OnboardsANewDomainInConfigAlone(t *testing.T) {
	t.Parallel()
	prom := fakeProm(t)

	// ── the operator's seven files, each through its production loader ──
	slos, err := rattle.LoadWatch(acmeDir("rattle", "watch.yaml"))
	if err != nil {
		t.Fatalf("load acme watch list: %v", err)
	}
	topo, err := whir.LoadCatalogFile(acmeDir("whir", "catalog-info.yaml"))
	if err != nil {
		t.Fatalf("load acme topology: %v", err)
	}
	stateQueries, err := whir.LoadStateQueries(acmeDir("whir", "state-queries.yaml"))
	if err != nil {
		t.Fatalf("load acme state queries: %v", err)
	}
	evidence, subjects, err := clank.LoadEvidenceQueries(acmeDir("whir", "evidence-queries.yaml"))
	if err != nil {
		t.Fatalf("load acme evidence queries: %v", err)
	}
	cat := configtest.CatalogAt(t, acmeDir("actions", "catalog.yaml"))
	classes := configtest.FailureClassesAt(t, acmeDir("actions", "failure-classes.yaml"))
	pol, err := hiss.LoadPolicy(acmeDir("hiss", "policy.yaml"))
	if err != nil {
		t.Fatalf("load acme policy: %v", err)
	}

	// rattle: the authored SLO is the steady-state contract, and it names the
	// dependencies whose health gates trusting a divergence on acme-api.
	if diff := cmp.Diff("acme-api", slos[0].Object); diff != "" {
		t.Error("the watch list's SLO must name acme's own service (-want +got)\n", diff)
	}

	// clank: the catalog is the file. An action the operator didn't author
	// cannot be proposed, and nothing compiled in leaks alongside theirs.
	var catalogued []string
	for _, c := range cat.Contracts() {
		catalogued = append(catalogued, c.Name)
	}
	if diff := cmp.Diff([]string{"acme-shed-load"}, catalogued); diff != "" {
		t.Error("the loaded catalog must hold exactly the authored action (-want +got)\n", diff)
	}

	// thump: the authored action is executable, not merely catalogued — the
	// binding comes from its execution block, so a ref reaches a real
	// mechanism with no Go edit.
	bound, err := actuate.BoundRefs(cat)
	if err != nil {
		t.Fatalf("acme's authored action names no executable mechanism: %v", err)
	}
	if !slices.Contains(bound, "acme-shed-load") {
		t.Fatalf("the authored action is inert: BoundRefs is %v", bound)
	}

	// ── the reason loop, over acme's own evidence ──
	model := &scriptedModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{
			{Name: "metrics", Args: json.RawMessage(`{"q":"acme_api_error_ratio"}`)},
			{Name: "metrics", Args: json.RawMessage(`{"q":"acme_db_connections_saturation"}`)},
		}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassServiceFailure,
			Hypotheses:   []proposal.Hypothesis{{Name: "acme_api_fault", Weight: 0.85}},
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "acme-shed-load", Confidence: 0.9,
				Citations: []string{"acme_api_error_ratio", "acme_db_connections_saturation"},
			}},
		})}}},
	}}

	sink := &publishtest.CapturePublisher[proposal.Set]{}
	engine := &clank.Engine{
		Intake: clank.NewIntake(
			clank.WhirTopology{
				Catalog:  topo,
				Resolver: &whir.Resolver{BaseURL: prom.URL, Queries: stateQueries},
			},
			noChange{},
		),
		Model: model,
		Tools: map[string]clank.Tool{"metrics": &clank.MetricsTool{
			BaseURL: prom.URL, Queries: evidence, Subjects: subjects,
		}},
		Catalog:        cat,
		FailureClasses: classes,
		Ranker:         clank.NewRanker(),
		Gate:           clank.ReadinessGate{},
		Store:          clank.NewMemStore(),
		Scorer:         clank.NewCausalScorer(),
		DedupeWindow:   time.Hour,
		Ledger:         clank.NewMemProposalLog(),
		Pub:            sink,
		MaxSteps:       8,
		Weights:        clank.DefaultScoringWeights(),
	}

	ctx := context.Background()
	sig := acmeSignal()
	set, err := engine.Propose(ctx, sig)
	if err != nil {
		t.Fatalf("Propose errored on the authored domain: %v", err)
	}

	// The topology the loop reasoned over came from the authored graph, and
	// the authored state queries resolved it — not a fallback, not unknown.
	var upstream []string
	for _, n := range set.SAOSnapshot.Topology.Upstream {
		upstream = append(upstream, n.Name+"="+n.State)
	}
	if diff := cmp.Diff([]string{"acme-db=healthy", "acme-cache=healthy"}, upstream); diff != "" {
		t.Error("the SAO's topology must come from the authored graph and state queries (-want +got)\n", diff)
	}

	if set.Gate == nil || !set.Gate.Passed {
		t.Fatalf("acme's evidence-backed signal must pass the gate: %+v", set.Gate)
	}
	if len(set.Proposals) == 0 {
		t.Fatal("a passed set must carry at least one proposal")
	}
	if diff := cmp.Diff("acme-shed-load", set.Proposals[0].ContractRef); diff != "" {
		t.Error("clank must propose the authored action (-want +got)\n", diff)
	}
	if len(sink.Delivered) != 1 {
		t.Errorf("a passed set is delivered to hiss exactly once; delivered %d", len(sink.Delivered))
	}

	// ── hiss governs it, under the authored policy ──
	now := time.Now()
	dec := hiss.Authority{}.Evaluate(set, pol, now)
	if diff := cmp.Diff(decision.VerdictApproved, dec.Verdict); diff != "" {
		t.Fatalf("the authored policy must approve the authored action (-want +got)\n%s\nreasons: %v\nconfidence %.2f vs floor %.2f",
			diff, dec.Reasons, set.Proposals[0].Confidence, dec.FloorApplied)
	}
	if diff := cmp.Diff("acme-v1", dec.PolicyVersion); diff != "" {
		t.Error("the decision must be stamped with the authored policy's version (-want +got)\n", diff)
	}

	// ── thump renders and dry-run executes it ──
	order, err := thump.Actuator{}.Render(decision.Governed{Decision: dec, Set: set}, cat, now)
	if err != nil {
		t.Fatalf("thump refused to render the authored action: %v", err)
	}
	if diff := cmp.Diff("acme-shed-load", order.ContractRef); diff != "" {
		t.Error("the rendered order must name the authored action (-want +got)\n", diff)
	}
	// The order's magnitude is the authored default, never a model-invented
	// number — the operator authored the range, so the operator bounds it.
	if diff := cmp.Diff(2.0, order.Parameters["serving_replicas"]); diff != "" {
		t.Error("the order's scope parameter must be the authored default (-want +got)\n", diff)
	}
	if diff := cmp.Diff("restore-acme-capacity", order.Reversal.Method); diff != "" {
		t.Error("the order must carry the authored reversal (-want +got)\n", diff)
	}

	oc := thump.DryRun{}.Execute(ctx, order, now)
	if diff := cmp.Diff(outcome.ModeDryRun, oc.Mode); diff != "" {
		t.Error("dry-run is the default: nothing in this test may touch a cluster (-want +got)\n", diff)
	}
	if diff := cmp.Diff(outcome.ResultRendered, oc.Result); diff != "" {
		t.Error("a dry-run's only terminal state is rendered (-want +got)\n", diff)
	}
}

// TestOperator_AnAuthoredActionNamingNoMechanismFailsAtLoad is the other half
// of the onboarding contract: config picks a bounded mechanism, it never
// describes a new one. An operator who invents a verb finds out at startup,
// not the first time governance approves the action.
func TestOperator_AnAuthoredActionNamingNoMechanismFailsAtLoad(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"BoundRefs rejects an authored action whose verb has no compiled mechanism": `
- name: acme-teleport
  execution:
    forward: [{verb: teleport, namespace: acme}]
    reverse: [{verb: teleport, namespace: acme}]`,
		"BoundRefs rejects an authored action that declares no undo": `
- name: acme-shed-load
  execution:
    forward: [{verb: scale, namespace: acme, deployment: acme-api, replicas: 2}]`,
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cat, err := contract.Load([]byte(doc), contract.Preconditions)
			if err != nil {
				t.Fatalf("load fixture catalog: %v", err)
			}
			if _, err := actuate.BoundRefs(cat); err == nil {
				t.Error("an authored action with no reachable mechanism must fail at load, got nil error")
			}
		})
	}
}
