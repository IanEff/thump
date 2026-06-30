package clank_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/clank"
)

func TestPropose_WithEvidence_YieldsARankedProposalSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		// turn 1: gather live evidence
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		// turn 2: propose - hypothesis + a candidate drawn from the catalog
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Hypotheses:   []clank.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
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

type fakeModel struct {
	script        []clank.Completion
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
	if m.i >= len(m.script) {
		return clank.Completion{}, nil // ran out of script -> no tool calls -> loop ends
	}
	c := m.script[m.i]
	m.i++
	return c, nil
}

func proposeArgs(t *testing.T, ps clank.ProposalSet) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}
	return b
}

type metricsTool struct{}

func (metricsTool) Run(_ context.Context, args json.RawMessage) (clank.EvidenceRef, error) {
	return clank.EvidenceRef{
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

type captureSink struct {
	delivered []clank.ProposalSet
}

func (s *captureSink) Deliver(_ context.Context, ps clank.ProposalSet) error {
	s.delivered = append(s.delivered, ps)
	return nil
}

func newTestEngine(model clank.Model) (*clank.Engine, *captureSink) {
	sink := &captureSink{}
	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: clank.TopologySnapshot{
				Downstream: []clank.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: clank.ChangeSnapshot{Events: []clank.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model: model,
		Tools: map[string]clank.Tool{"metrics": metricsTool{}},
		Catalog: clank.NewStaticCatalog([]clank.ActionContract{{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []clank.FailureClass{clank.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
		}}),
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		Ledger:       clank.NewMemProposalLog(),
		Sink:         sink,
		MaxSteps:     8,
		Policy:       clank.GatePolicy{},
	}, sink
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
}

func (f fakeTool) Run(_ context.Context, _ json.RawMessage) (clank.EvidenceRef, error) {
	return clank.EvidenceRef{Tool: f.name, Summary: f.digest, Ref: f.ref, Live: f.live}, nil
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

func openProposalFor(fp string) clank.ProposalSet {
	return clank.ProposalSet{
		SignalRef: fp,
		Status:    &clank.ProposalStatus{Phase: "proposed"},
	}
}

func TestPropose_WhenModelDeclines_YieldsNoAction(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "insufficient"}}},
	}}
	e, sink := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if diff := cmp.Diff("no_action", got.Status.Phase); diff != "" {
		t.Error("a declined investigation should be no_action (-want +got)\n", diff)
	}
	if len(sink.delivered) != 0 {
		t.Errorf("no_action must deliver nothing: delivered %d", len(sink.delivered))
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
	if len(sink.delivered) != 0 {
		t.Errorf("budget_exhausted delivers nothing %d", len(sink.delivered))
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
		{ToolCalls: []clank.ToolCall{{Name: "insufficient"}}},
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
	model := &fakeModel{script: []clank.Completion{{ToolCalls: []clank.ToolCall{{Name: "insufficient"}}}}}
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
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Proposals:    []clank.Candidate{{ID: "neerdowell", ContractRef: "rm -rf"}},
		})}}},
	}}

	e, sink := newTestEngine(model)
	_, err := e.Propose(context.Background(), sigBurnAccel())
	if !errors.Is(err, clank.ErrOutsideCatalog) {
		t.Fatalf("a contract the catalog doesn't list must be rejected: got %v", err)
	}
	if len(sink.delivered) != 0 {
		t.Errorf("a rejected set must never be delivered: %d", len(sink.delivered))
	}
}

func TestPropose_SuppressesAnOpenDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sig := sigBurnAccel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.89}},
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
	if len(sink.delivered) != 0 {
		t.Errorf("a suppressed set is recorded, not delivered: %d", len(sink.delivered))
	}
}

func TestPropose_FreezesTheSAOIntoTheSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths"}},
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
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths"}},
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
