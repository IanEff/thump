package clank_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish/publishtest"
)

func TestPropose_WithEvidence_YieldsARankedProposalSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		// turn 1: gather live evidence
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		// turn 2: propose - hypothesis + a candidate drawn from the catalog
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	if !got.Gate.Passed || len(got.Proposals) == 0 {
		t.Fatalf("an evidence-backed signal should yield a passed, non-empty ProposalSet: %+v", got)
	}
	if diff := cmp.Diff(got.Proposals[0].ID, got.Recommended); diff != "" {
		t.Error("recommended must be the rank-1 proposal (-want +got)\n", diff)
	}
}

// TestPropose_GateDeclineSurfacesReason pins the other half of the "mute
// decline" bug: a model that DOES propose (unlike the insufficient-evidence
// path TestGoldenPath_ArgocdSyncDeclinesWithALegibleReason exercises) and
// cites evidence it actually gathered — so the citation check clears — but
// that evidence is never Live, so the readiness gate itself declines. That
// decline must say why (Status.Reason), not just what (Status.Phase).
func TestPropose_GateDeclineSurfacesReason(t *testing.T) {
	t.Parallel()
	tool := fakeTool{name: "logs", digest: "no live signal", ref: "loki:xyz", live: false, query: "log_scan"}
	model := &fakeModel{script: []clank.Completion{
		// turn 1: gather evidence that is never Live
		{ToolCalls: []clank.ToolCall{{Name: "logs", Args: json.RawMessage(`{"q":"log_scan"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{"log_scan"}}},
		})}}},
	}}

	e, sink := newTestEngine(model)
	e.Tools = map[string]clank.Tool{"logs": tool}
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	if got.Gate.Passed {
		t.Fatalf("no live evidence should fail the gate, got Passed=true: %+v", got.Gate)
	}
	if len(sink.Delivered) != 0 {
		t.Fatalf("a gate decline delivers nothing; delivered %d", len(sink.Delivered))
	}
	if got.Status.Phase != "no_action" {
		t.Errorf("a gate decline is phase=no_action, got %q", got.Status.Phase)
	}
	if got.Status.Reason == "" {
		t.Fatal("gate decline is mute: Status.Reason is empty despite GateResult.Reason being set")
	}
	if diff := cmp.Diff(got.Gate.Reason, got.Status.Reason); diff != "" {
		t.Error("Status.Reason must mirror GateResult.Reason (-want +got)\n", diff)
	}
}

func TestPropose_StampsReversalAndBandFromTheCatalog(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			// bare — no ReversalPath, no GovernanceLevel, exactly what production omits
			Proposals: []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	cand := got.Proposals[0]
	if cand.ReversalPath == nil {
		t.Fatal("a catalogued, reversible action must have ReversalPath stamped, got nil")
	}
	if diff := cmp.Diff("unthrottle", cand.ReversalPath.Method); diff != "" {
		t.Error("ReversalPath.Method must come from the contract's Reversal.Method (-want +got)", diff)
	}
	if cand.GovernanceLevel == nil {
		t.Fatal("a reversible action must have GovernanceLevel stamped, got nil")
	}
	if diff := cmp.Diff(string(decision.BandActReversible), cand.GovernanceLevel.Band); diff != "" {
		t.Error("a reversible contract requests act_reversible (-want +got)", diff)
	}
}

// TestPropose_IrreversibleContractLeavesReversalNil is the honesty rider:
// stamping must never INVENT a reversal an action doesn't have — that would
// defeat hiss's I-12 irreversibility veto. An authored action with an empty
// Reversal must come out of Propose with ReversalPath still nil.
func TestPropose_IrreversibleContractLeavesReversalNil(t *testing.T) {
	t.Parallel()
	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "cordon-node",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		// Reversal deliberately zero-value — this action genuinely can't be undone
	}})

	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "cordon-node", Confidence: 0.9, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngineWithCatalog(model, cat)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	cand := got.Proposals[0]
	if cand.ReversalPath != nil {
		t.Errorf("an action with no authored Reversal must not get a fabricated ReversalPath, got %+v", cand.ReversalPath)
	}
	if cand.GovernanceLevel == nil || cand.GovernanceLevel.Band != string(decision.BandActDisruptive) {
		t.Errorf("an irreversible action's requested band must be act_disruptive, got %+v", cand.GovernanceLevel)
	}
}

// TestPropose_StampsPredictedImpactFromTheCatalog pins the producer half of
// the effectiveness delta: enrichFromCatalog copies the authored
// SeverityReductionPct onto the candidate the same way it copies BlastTier and
// the reversal, so recordEffectiveness has a forecast to score the observed
// reduction against. hold-rebalance authors a 0.7 baseline.
func TestPropose_StampsPredictedImpactFromTheCatalog(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"ceph_health"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassRedundancyDegraded,
			Hypotheses:   []proposal.Hypothesis{{Name: "redundancy_degraded", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "hold-rebalance", Confidence: 0.82, Citations: []string{`{"q":"ceph_health"}`}}},
		})}}},
	}}

	e, _ := newTestEngineWithCatalog(model, contract.Default())
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	cand := got.Proposals[0]
	if cand.PredictedImpact == nil {
		t.Fatal("a catalogued action with an authored SeverityReductionPct must have PredictedImpact stamped, got nil")
	}
	if diff := cmp.Diff(0.7, cand.PredictedImpact.SeverityReductionPct); diff != "" {
		t.Error("PredictedImpact.SeverityReductionPct must come from the contract's authored baseline (-want +got)", diff)
	}
}

// TestPropose_UnforecastContractLeavesPredictedImpactNil is the effectiveness
// honesty rider, mirroring the reversal one: an action the catalog gives no
// SeverityReductionPct must come out of Propose with PredictedImpact nil — a
// zero baseline is unforecast, not a forecast of no effect, so
// recordEffectiveness skips it rather than scoring a fabricated win.
func TestPropose_UnforecastContractLeavesPredictedImpactNil(t *testing.T) {
	t.Parallel()
	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "cordon-node",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		Reversal:                 contract.Reversal{Method: "uncordon-node"},
		// SuccessCriteria.SeverityReductionPct deliberately zero — this action forecasts nothing
	}})

	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "cordon-node", Confidence: 0.9, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngineWithCatalog(model, cat)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	if cand := got.Proposals[0]; cand.PredictedImpact != nil {
		t.Errorf("an action with no authored SeverityReductionPct must not get a fabricated PredictedImpact, got %+v", cand.PredictedImpact)
	}
}

type fakeModel struct {
	script        []clank.Completion
	err           error // when set, Complete fails on every call regardless of script — simulates a Model outage
	i             int
	calls         int
	received      [][]clank.Message
	receivedTools [][]clank.ToolSpec
}

func (m *fakeModel) Complete(_ context.Context, msgs []clank.Message, tools []clank.ToolSpec) (clank.Completion, error) {
	m.calls++
	cp := make([]clank.Message, len(msgs))
	copy(cp, msgs)
	m.received = append(m.received, cp)
	m.receivedTools = append(m.receivedTools, tools)
	if m.err != nil {
		return clank.Completion{}, m.err
	}
	if m.i >= len(m.script) {
		return clank.Completion{}, nil // ran out of script -> no tool calls -> loop ends
	}
	c := m.script[m.i]
	m.i++
	return c, nil
}

func proposeArgs(t *testing.T, ps proposal.Set) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}
	return b
}

type metricsTool struct{}

func (metricsTool) Run(_ context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{
		Tool:    "metrics",
		Query:   string(args),
		Summary: "latency_p99 elevated 3x over baseline",
		Ref:     "metrics://latency_p99",
		Live:    true,
	}, nil
}

func (metricsTool) Spec() clank.ToolSpec {
	return clank.ToolSpec{Name: "metrics", Description: "read-only telemetry query"}
}

func newTestEngine(model clank.Model) (*clank.Engine, *publishtest.CapturePublisher[proposal.Set]) {
	pub := &publishtest.CapturePublisher[proposal.Set]{}
	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: proposal.TopologySnapshot{
				Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model: model,
		Tools: map[string]clank.Tool{"metrics": metricsTool{}},
		Catalog: contract.NewStaticCatalog([]contract.ActionContract{{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Reversal:                 contract.Reversal{Method: "unthrottle", Fallback: "page-oncall"},
		}}),
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

func newTestEngineWithCatalog(model clank.Model, cat *contract.StaticCatalog) (*clank.Engine, *publishtest.CapturePublisher[proposal.Set]) {
	pub := &publishtest.CapturePublisher[proposal.Set]{}
	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: proposal.TopologySnapshot{
				Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model:        model,
		Tools:        map[string]clank.Tool{"metrics": metricsTool{}},
		Catalog:      cat,
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

type failingStore struct {
	*clank.MemStore
	failOn int
	calls  int
}

func (f *failingStore) Checkpoint(ctx context.Context, t clank.Turn) error {
	if f.calls == f.failOn {
		f.calls++
		return errors.New("disk on fire")
	}
	f.calls++
	return f.MemStore.Checkpoint(ctx, t)
}

type fakeTool struct {
	name   string
	digest string
	ref    string
	live   bool
	query  string
}

func (f fakeTool) Run(_ context.Context, _ json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{Tool: f.name, Summary: f.digest, Ref: f.ref, Live: f.live, Query: f.query}, nil
}

func (f fakeTool) Spec() clank.ToolSpec {
	return clank.ToolSpec{Name: f.name, Description: "read-only"}
}

func specsContain(specs []clank.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

func specNames(specs []clank.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

func openProposalFor(fp string) proposal.Set {
	return proposal.Set{
		SignalRef: fp,
		Status:    &proposal.Status{Phase: "proposed"},
	}
}

func TestPropose_WhenModelDeclines_YieldsNoAction(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{
			Name: "insufficient",
			Args: json.RawMessage(`{"reason":"no live corroboration for the topology hypothesis"}`),
		}}},
	}}
	e, sink := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if diff := cmp.Diff("no_action", got.Status.Phase); diff != "" {
		t.Error("a declined investigation should be no_action (-want +got)\n", diff)
	}
	if diff := cmp.Diff("no live corroboration for the topology hypothesis", got.Status.Reason); diff != "" {
		t.Error("a reasoned decline must carry its reason (-want +got)\n", diff)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("no_action must deliver nothing: delivered %d", len(sink.Delivered))
	}
}

func TestPropose_StopsAtMaxSteps_YieldsBudgetExhausted(t *testing.T) {
	t.Parallel()
	metrics := clank.Completion{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}}
	model := &fakeModel{script: []clank.Completion{metrics, metrics, metrics, metrics}}
	e, sink := newTestEngine(model)

	e.MaxSteps = 3

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if got.Gate.BudgetOK {
		t.Errorf("exhausting MaxSteps must fail the budget minimum: %+v", got.Gate)
	}
	if diff := cmp.Diff("budget_exhausted", got.Status.Phase); diff != "" {
		t.Error("falling out of the loop should be budget_exhausted (-want +got)\n", diff)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("budget_exhausted delivers nothing %d", len(sink.Delivered))
	}
}

func TestPropose_HaltsWhenCheckpointFails(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"y"}`)}}}, // never reached
	}}
	e, _ := newTestEngine(model)
	e.Store = &failingStore{MemStore: clank.NewMemStore(), failOn: 0}
	_, err := e.Propose(context.Background(), sigBurnAccel())
	if err == nil {
		t.Fatal("a checkpoint failure must halt Propose with an error")
	}
	if model.calls != 1 {
		t.Errorf("run must halt at the failed checkpoint, not proceed: model.calls=%d", model.calls)
	}
}

func TestPropose_AppendsTheToolDigestToTheConversation(t *testing.T) {
	t.Parallel()
	const digest = "503 rate 12%/min on /checkout"
	tool := fakeTool{name: "logs", digest: digest, ref: "loki:abc", live: true}
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "logs", Args: json.RawMessage(`{"q":"errors"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"stub"}`)}}},
	}}
	e, _ := newTestEngine(model)
	e.Tools = map[string]clank.Tool{"logs": tool}

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	// Inv. 1 (digests only): a read-only tool's one-line EvidenceRef.Summary is
	// what enters the conversation — and that's *all* that can, since EvidenceRef
	// has no Raw field. This asserts the positive: the digest reached the model as
	// a tool-role message. (The old form scanned for a sentinel no tool ever
	// emitted, so it could never fail — a vacuous test with no teeth.)
	if !receivedToolDigest(model.received, digest) {
		t.Errorf("tool digest %q never reached the conversation:\n%+v", digest, model.received)
	}
}

func TestPropose_WhenModelEndsTurnWithoutATool_YieldsSyntheticReason(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{Message: clank.Message{Role: "assistant", Content: "I'm not sure what to do here."}},
	}}
	e, sink := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if diff := cmp.Diff("no_action", got.Status.Phase); diff != "" {
		t.Error("an empty-handed turn should still be no_action (-want +got)\n", diff)
	}
	if diff := cmp.Diff("model ended turn without a tool call", got.Status.Reason); diff != "" {
		t.Error("an empty-handed turn needs its own synthetic reason (-want +got)\n", diff)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("no_action must deliver nothing: delivered %d", len(sink.Delivered))
	}
}

// receivedToolDigest reports whether any message snapshot shown to the model
// carries the digest as a tool-role message — i.e. the engine forwarded the
// one-line EvidenceRef.Summary into the conversation.
func receivedToolDigest(snapshots [][]clank.Message, digest string) bool {
	for _, msgs := range snapshots {
		for _, m := range msgs {
			if m.Role == "tool" && m.Content == digest {
				return true
			}
		}
	}
	return false
}

func TestPropose_OffersReadOnlyToolsAndControlVerbs(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{{ToolCalls: []clank.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"stub"}`)}}}}}
	e, _ := newTestEngine(model)
	e.Tools = map[string]clank.Tool{
		"metrics":  metricsTool{},
		"casebase": fakeTool{name: "casebase", digest: "similar incident 3w ago", ref: "cb:1"},
	}

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	if len(model.receivedTools) == 0 {
		t.Fatal("model wasn't offered any tool specs")
	}
	offered := model.receivedTools[0]

	// A real model can only emit a tool call for a tool it was offered, so the
	// read-only telemetry tools AND the two terminal control verbs must all be
	// on the table — otherwise the loop can never terminate via propose/insufficient.
	for _, name := range []string{"metrics", "casebase", "propose", "insufficient"} {
		if !specsContain(offered, name) {
			t.Errorf("expected %q to be offered to the model: %v", name, specNames(offered))
		}
	}

	// The autonomy boundary: a catalogued action is never a callable tool. The
	// model names it by ref inside propose's args, where enforceCatalog gates it.
	if specsContain(offered, "throttle-non-critical-paths") {
		t.Errorf("a catalogued action must not be offered as a callable tool: %v", specNames(offered))
	}
}

func TestPropose_RejectsACandidateOutsideTheCatalog(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "neerdowell", ContractRef: "rm -rf"}},
		})}}},
	}}

	e, sink := newTestEngine(model)
	_, err := e.Propose(context.Background(), sigBurnAccel())
	if !errors.Is(err, contract.ErrOutsideCatalog) {
		t.Fatalf("a contract the catalog doesn't list must be rejected: got %v", err)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a rejected set must never be delivered: %d", len(sink.Delivered))
	}
}

// TestPropose_ClassMismatchBecomesAnAuditableDecline pins the fix for the
// 2026-07-13 discrimination bug (thump-running-notes.md): unlike a wholly
// invented ContractRef (the test above), "hold-rebalance" here IS a real
// catalogued action — it's just not applicable to the class the model
// declared. That must become a legible no_action decline recorded to the
// ledger, never a returned error (which would leave the whole run
// unaudited) and never delivered.
func TestPropose_ClassMismatchBecomesAnAuditableDecline(t *testing.T) {
	t.Parallel()
	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "hold-rebalance",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassResourceExhaustion},
		ApplicableTiers:          []string{"tier-1"},
	}})
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassUnknown,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "hold-rebalance"}},
		})}}},
	}}

	e, sink := newTestEngineWithCatalog(model, cat)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("a class mismatch must not error the whole run, got %v", err)
	}
	if got.Status.Phase != "no_action" {
		t.Errorf("phase = %q, want no_action", got.Status.Phase)
	}
	if got.Status.Reason == "" {
		t.Fatal("a class-mismatch decline is mute: Status.Reason is empty")
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a declined set must never be delivered: %d", len(sink.Delivered))
	}
}

func TestPropose_RecordsTheCitationsEachCandidateCarries(t *testing.T) {
	t.Parallel()

	// The citation list must survive the round trip untouched: what the model
	// cited is what the audit trail carries — the gate and the confidence
	// function read this list, so a dropped or reordered citation would change
	// what the machine believes it verified.
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.9,
				Citations: []string{`{"q":"latency_p99"}`},
			}},
		})}}},
	}}

	eng, _ := newTestEngine(model)
	got, err := eng.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{`{"q":"latency_p99"}`}
	if diff := cmp.Diff(want, got.Proposals[0].Citations); diff != "" {
		t.Error("candidate citations did not survive the round trip (-want +got)\n", diff)
	}
}

func TestPropose_DeclinesACandidateCitingEvidenceTheRunNeverGathered(t *testing.T) {
	t.Parallel()

	// A citation naming a query the loop never issued is a causal claim with
	// no inspectable basis — the run must end as an auditable no_action, never
	// a delivered set. This is the same refusal shape as a class-mismatched
	// contract ref: recorded and loud, not silent.
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.9,
				Citations: []string{`{"q":"a_query_never_issued"}`},
			}},
		})}}},
	}}

	eng, sink := newTestEngine(model)
	got, err := eng.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != proposal.PhaseNoAction {
		t.Errorf("an ungrounded citation must decline, got phase %q (reason %q)", got.Status.Phase, got.Status.Reason)
	}
	if len(sink.Delivered) != 0 {
		t.Error("an ungrounded citation must never be delivered", cmp.Diff(0, len(sink.Delivered)))
	}
}

func TestPropose_SuppressesAnOpenDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sig := sigBurnAccel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.89, Citations: []string{`{"q":"x"}`}}},
		})}}},
	}}
	e, sink := newTestEngine(model)

	if err := e.Ledger.Record(ctx, openProposalFor(sig.Fingerprint)); err != nil {
		t.Fatal(err)
	}
	got, err := e.Propose(ctx, sig)
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if got.Gate.DedupeOK {
		t.Errorf("an open proposal on the same fingerprint must fail dedupe: %+v", got.Gate)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a suppressed set is recorded, not delivered: %d", len(sink.Delivered))
	}
}

func TestPropose_FreezesTheSAOIntoTheSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Citations: []string{`{"q":"x"}`}}},
		})}}},
	}}
	e, _ := newTestEngine(model)

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}
	if got.SAOSnapshot.Version == 0 {
		t.Errorf("the SAO must be frozen onto the set for audit replay: %+v", got.SAOSnapshot)
	}
}

func TestPropose_AttachesCausalScoresToTheSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Citations: []string{`{"q":"x"}`}}},
		})}}},
	}}
	e, _ := newTestEngine(model)

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	if len(got.CausalScores) == 0 {
		t.Fatalf("the engine must score the change events onto the audit unit: %+v", got)
	}
	for _, cs := range got.CausalScores {
		if len(cs.Rationale) == 0 {
			t.Errorf("every causal score must carry its rationale, not just a number: %v", cs)
		}
	}
}
