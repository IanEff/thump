package clank_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/thump"
	"sigs.k8s.io/yaml"
)

// The golden-path suite is WS1.6 Stage 4 — the keystone. Unlike the eval
// harness (eval_test.go, a real model, key-gated, never in CI), this binds
// the loop to the PRODUCTION catalog (defaultCatalog) and to committed real
// fixtures, with a SCRIPTED model, and pins every boundary object to a
// checked-in golden. It proves the deterministic machine — govern → act →
// learn — closes on the actions clank actually ships, and stays closed on
// every `make ci`. Whether a *real* model proposes is the eval harness's
// non-deterministic question; a golden file can't assert an LLM.
//
// goldenNow is the single fixed clock threaded into every beat that stamps a
// time, so the goldens are stable. The one field NOT under this clock is the
// SAO's AssembledAt (intake uses time.Now() directly, no clock seam) — it's
// scrubbed to zero before the golden marshal; see scrubVolatile.
var goldenNow = time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

func TestGoldenPath_NodeDeathClosesTheLoopOnTheProductionCatalog(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	det := loadDetectionFixtureExt(t, "node-death.yaml")

	// scripted model: step 1 gather LIVE evidence (metricsTool → Live:true,
	// clears the gate's evidenceOK); step 2 propose hold-rebalance — a
	// catalogued action for the fixture's class+tier — carrying a
	// ReversalPath (or hiss Claim 5 vetoes) and a requested band (or the
	// grant defaults to observe).
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"ceph_health"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassResourceExhaustion, // in defaultCatalog's hold-rebalance
			Hypotheses:   []clank.Hypothesis{{Name: "osd_capacity_loss", Weight: 0.9}},
			Proposals: []clank.Candidate{{
				ID: "p1", ContractRef: "hold-rebalance", Confidence: 0.9,
				ReversalPath: &clank.ReversalPath{
					Method: "release-rebalance", Watching: "ceph_health", Trigger: "HEALTH_OK",
				},
				GovernanceLevel: &clank.GovernanceLevel{Band: string(hiss.BandActReversible)},
			}},
		})}}},
	}}

	eng, sink := goldenEngine(model)

	// ── beat one+two: reason → deliver ──────────────────────────────────
	set, err := eng.Propose(ctx, det)
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if set.Gate == nil || !set.Gate.Passed {
		t.Fatalf("the production catalog should serve a node death: gate=%+v", set.Gate)
	}
	if len(sink.delivered) != 1 {
		t.Fatalf("a passed set is delivered exactly once; delivered %d", len(sink.delivered))
	}
	delivered := sink.delivered[0]
	if delivered.Recommended != "p1" || delivered.Proposals[0].ContractRef != "hold-rebalance" {
		t.Fatalf("recommended action should be hold-rebalance, got %q (%s)",
			delivered.Proposals[0].ContractRef, delivered.Recommended)
	}
	scrubVolatile(&delivered)
	assertGolden(t, "node-death-proposal.yaml", delivered)

	// ── beat three: govern ──────────────────────────────────────────────
	dec := hiss.Authority{}.Evaluate(delivered, goldenPolicy(), goldenNow)
	if err := dec.Auditable(); err != nil {
		t.Fatal("decision must be auditable:", err)
	}
	if dec.Verdict != hiss.VerdictApproved {
		t.Fatalf("hiss must approve the golden path: %s (reasons: %v)", dec.Verdict, dec.Reasons)
	}
	assertGolden(t, "node-death-decision.yaml", dec)

	// ── beat four: render + rehearse (dry-run, nothing executed) ────────
	order, err := thump.Actuator{}.Render(
		decision.Governed{Decision: dec, Set: delivered}, goldenCatalog(), goldenNow)
	if err != nil {
		t.Fatal("thump.Render errored:", err)
	}
	out := thump.DryRun{}.Execute(ctx, order, goldenNow)
	if out.Result != thump.ResultRendered {
		t.Fatalf("a dry-run ends rendered, not executed: %s", out.Result)
	}
	assertGolden(t, "node-death-outcome.yaml", out)

	// ── beat five: learn — bank the rehearsal, belief UNMOVED ───────────
	cb := clank.NewCaseBase()
	click := clank.Click{Ledger: eng.Ledger, Cases: cb}
	if err := click.Absorb(ctx, out); err != nil {
		t.Fatal("click.Absorb errored:", err)
	}
	cases := cb.Cases(det.Fingerprint)
	if len(cases) != 1 {
		t.Fatalf("one outcome, one case; got %d", len(cases))
	}
	assertGolden(t, "node-death-case.yaml", cases[0])

	// a rehearsal is bookkeeping, not evidence: nothing may be believed yet.
	if _, corroborated := cb.Alignment(det.Fingerprint); corroborated {
		t.Error("the loop closed on a dry-run — the prior must stay untouched")
	}
}

// TestGoldenPath_DedupOnReplaySuppressesTheSecondSet pins the ledger's
// fingerprint dedup: an already-open set for a fingerprint suppresses a fresh
// one for the same fingerprint (recorded, NOT delivered). This is the
// property the 2026-07-04 live replay experiment proved by hand — now a CI
// regression pin.
func TestGoldenPath_DedupOnReplaySuppressesTheSecondSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	det := loadDetectionFixtureExt(t, "node-death.yaml")

	eng, sink := goldenEngine(nil) // model set per-call below

	// first pass: a full propose, delivered and left OPEN in the ledger.
	eng.Model = goldenNodeDeathModel(t)
	if _, err := eng.Propose(ctx, det); err != nil {
		t.Fatalf("first Propose errored: %v", err)
	}
	if len(sink.delivered) != 1 {
		t.Fatalf("first pass should deliver once; got %d", len(sink.delivered))
	}

	// second pass: same fingerprint, same engine (same ledger). The open set
	// from pass one suppresses this one — gated to no_action, NOT delivered.
	eng.Model = goldenNodeDeathModel(t)
	set2, err := eng.Propose(ctx, det)
	if err != nil {
		t.Fatalf("second Propose errored: %v", err)
	}
	if len(sink.delivered) != 1 {
		t.Fatalf("replay must be suppressed, not delivered; delivered %d total", len(sink.delivered))
	}
	if set2.Gate == nil || set2.Gate.Passed {
		t.Errorf("the replay's gate must fail on dedup: %+v", set2.Gate)
	}
	if set2.Status.Phase != "no_action" {
		t.Errorf("a suppressed replay is phase=no_action, got %q", set2.Status.Phase)
	}
}

// TestGoldenPath_ArgocdSyncDeclinesWithALegibleReason is Stage 1's payoff as
// a regression pin: a fixture the production catalog genuinely can't serve
// declines to no_action AND says why. A mute decline (the round-1 pain) fails
// here.
func TestGoldenPath_ArgocdSyncDeclinesWithALegibleReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	det := loadDetectionFixtureExt(t, "argocd-sync-burn.yaml")

	const reason = "no catalogued action revives a stalled GitOps sync"
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "insufficient", Args: json.RawMessage(
			`{"reason":"` + reason + `"}`)}}},
	}}

	eng, sink := goldenEngine(model)
	set, err := eng.Propose(ctx, det)
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if len(sink.delivered) != 0 {
		t.Fatalf("a decline delivers nothing; delivered %d", len(sink.delivered))
	}
	if set.Status.Phase != "no_action" {
		t.Errorf("a decline is phase=no_action, got %q", set.Status.Phase)
	}
	if set.Status.Reason == "" {
		t.Fatal("Stage 1 regressed: the decline is mute again")
	}
	assertGolden(t, "argocd-sync-status.yaml", set.Status)
}

// ── helpers ─────────────────────────────────────────────────────────────

// goldenNodeDeathModel is the node-death propose script, minted fresh per
// call (fakeModel carries call-index state, so a replay test needs a new one
// each pass).
func goldenNodeDeathModel(t *testing.T) *fakeModel {
	t.Helper()
	return &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"ceph_health"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassResourceExhaustion,
			Hypotheses:   []clank.Hypothesis{{Name: "osd_capacity_loss", Weight: 0.9}},
			Proposals: []clank.Candidate{{
				ID: "p1", ContractRef: "hold-rebalance", Confidence: 0.9,
				ReversalPath: &clank.ReversalPath{
					Method: "release-rebalance", Watching: "ceph_health", Trigger: "HEALTH_OK",
				},
				GovernanceLevel: &clank.GovernanceLevel{Band: string(hiss.BandActReversible)},
			}},
		})}}},
	}}
}

// goldenEngine wires an Engine on the PRODUCTION catalog (the whole point of
// this suite). Topology/change sources are empty so intake falls back to the
// Detection's own observed topology — the realistic path Main takes today
// with noop sources. A nil model may be set later via eng.Model.
func goldenEngine(model clank.Model) (*clank.Engine, *capturePublisher) {
	pub := &capturePublisher{}
	return &clank.Engine{
		Intake:       clank.NewIntake(fakeTopo{}, fakeChange{}),
		Model:        model,
		Tools:        map[string]clank.Tool{"metrics": metricsTool{}},
		Catalog:      clank.DefaultCatalogForTest(),
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		Ledger:       clank.NewMemProposalLog(),
		Pub:          pub,
		MaxSteps:     8,
	}, pub
}

// goldenPolicy is a hiss policy that approves the node-death path: the
// tier-1 × resource_exhaustion floor sits below the 0.9 candidate confidence,
// and the tier-1 ceiling admits the requested act_reversible band.
func goldenPolicy() hiss.Policy {
	return hiss.Policy{
		Version: "golden-v1",
		Floors: map[string]map[clank.FailureClass]float64{
			"tier-1": {clank.ClassResourceExhaustion: 0.75},
		},
		MaxBand:         map[string]hiss.Band{"tier-1": hiss.BandActReversible},
		RequireReversal: true,
	}
}

// goldenCatalog is thump's leg's catalog. thump resolves the granted
// ContractRef from its OWN compiled-in copy, which clank.go:161-164 mandates
// is byte-identical to clank's — so reusing the production catalog here is
// exactly what a correct thump would resolve against.
func goldenCatalog() *clank.StaticCatalog {
	return clank.DefaultCatalogForTest()
}

func loadDetectionFixtureExt(t *testing.T, name string) signal.Detection {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "detections", name)) //nolint:gosec
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var det signal.Detection
	if err := yaml.Unmarshal(raw, &det); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return det
}

// scrubVolatile zeroes the one field the golden clock can't reach: the SAO's
// AssembledAt, stamped with time.Now() in intake (no clock seam there yet).
// Everything else on the set is either static or under goldenNow.
func scrubVolatile(set *clank.ProposalSet) {
	if set.SAOSnapshot != nil {
		set.SAOSnapshot.AssembledAt = time.Time{}
	}
}

// assertGolden marshals v to YAML and compares it to testdata/golden/<name>.
// Run `go test ./internal/clank -run GoldenPath -update` to (re)generate.
// The -update flag is shared with schema_test.go (same test package).
func assertGolden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal golden %s: %v", name, err)
	}
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("update golden %s: %v", name, err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // G304: fixed testdata path, not user input
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create it): %v", name, err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s drifted from golden (-want +got):\n%s", name, diff)
	}
}
