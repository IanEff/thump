package clank_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/clank"
)

func TestGate(t *testing.T) {
	t.Parallel()
	withEvidence := clank.Decision{
		Evidence: []clank.EvidenceRef{{Summary: "503 rate 12%/min on /checkout", Ref: "loki:abc"}},
	}
	noEvidence := clank.Decision{Evidence: nil}

	cases := []struct {
		name      string
		decision  clank.Decision
		openDupes []clank.Outcome
		want      clank.Verdict
	}{
		{
			name:     "rejects a decision with no evidence",
			decision: noEvidence,
			want:     clank.Verdict{Admit: false, Status: clank.StatusInsufficientEvidence},
		},
		{
			name:      "suppresses a decision that already has an open proposal",
			decision:  withEvidence,
			openDupes: []clank.Outcome{{Status: clank.StatusProposed}},
			want:      clank.Verdict{Admit: false, Status: clank.StatusSuppressedDuplicate},
		},
		{
			name:     "admits a decision with evidence and no duplicate",
			decision: withEvidence,
			want:     clank.Verdict{Admit: true, Status: clank.StatusProposed},
		},
	}

	var gate clank.ReadinessGate
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := gate.Evaluate(tc.decision, tc.openDupes)
			if got.Admit != tc.want.Admit || got.Status != tc.want.Status {
				t.Errorf("gate returned the wrong verdict for the decision\n%s", cmp.Diff(tc.want, got))
			}
		})
	}
}

func TestProposalLog_OpenRespectsTheDedpWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := clank.NewMemProposalLog()

	at := time.Now()
	err := log.Record(ctx, clank.Outcome{
		Decision: clank.Decision{RunID: "r1", Fingerprint: "fp-1"},
		Status:   clank.StatusProposed,
		At:       at,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := log.Open(ctx, "fp-1", at.Add(-time.Hour))
	if len(got) != 1 {
		t.Errorf("a proposal recorded inside the window shouild be open: want 1, got %d", len(got))
	}

	got, _ = log.Open(ctx, "fp-1", at.Add(time.Hour))
	if len(got) != 0 {
		t.Errorf("a proposal older than `since` should not be open: want 0, got %d", len(got))
	}
}

func TestStore_PendingReturnsACheckpointedTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := clank.NewMemStore()

	want := clank.Turn{RunID: "r1", Step: 0, Msgs: []clank.Message{{Role: "user", Content: "hi"}}}
	if err := store.Checkpoint(ctx, want); err != nil {
		t.Fatal(err)
	}
	pending, _ := store.Pending(ctx)
	if len(pending) != 1 || pending[0].RunID != "r1" {
		t.Errorf("a checkpointed turn should come bac as pending: want [r1], got %+v", pending)
	}
}

type fakeModel struct {
	script   []clank.Completion
	calls    int
	received [][]clank.Message
}

func (m *fakeModel) Complete(_ context.Context, msgs []clank.Message, _ []clank.ToolSpec) (clank.Completion, error) {
	m.received = append(m.received, msgs)
	if m.calls >= len(m.script) {
		return clank.Completion{}, fmt.Errorf("fakeModel: script exhausted after %d calls", m.calls)
	}
	c := m.script[m.calls]
	m.calls++
	return c, nil
}

// type failingStore struct {
// 	*clank.MemStore
// 	failOn int
// 	n      int
// }

// func (s *failingStore) Checkpoint(ctx context.Context, t clank.Turn) error {
// 	s.n++
// 	if s.n == s.failOn {
// 		return fmt.Errorf("failingStore: simulated checkpoint failure on call %d", s.n)
// 	}
// 	return s.MemStore.Checkpoint(ctx, t)
// }

type recordingSink struct{ delivered []clank.Outcome }

func (s *recordingSink) Deliver(_ context.Context, o clank.Outcome) error {
	s.delivered = append(s.delivered, o)
	return nil
}

type fakeTool struct {
	name   string
	digest string
	ref    string
	// rawLeaks bool
}

func (f fakeTool) Name() string         { return f.name }
func (f fakeTool) Spec() clank.ToolSpec { return clank.ToolSpec{Name: f.name} }

func (f fakeTool) Run(_ context.Context, _ json.RawMessage) (clank.EvidenceRef, error) {
	return clank.EvidenceRef{Tool: f.name, Summary: f.digest, Ref: f.ref}, nil
}

func testSignal() clank.Signal {
	return clank.Signal{
		ID:          "1",
		Fingerprint: "fp-1",
	}
}

// Happy path.
func TestPropose_WithEvidence_YieldsAProposal(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "logs", Args: json.RawMessage(`{"q":"503s}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.Decision{
			Action:   "scale checkout to 6",
			Evidence: []clank.EvidenceRef{{Summary: "503 rate 12%/min", Ref: "loki:abc"}},
		})}}},
	}}

	e := newTestEngine(model)

	got, err := e.Propose(context.Background(), testSignal())
	if err != nil {
		t.Fatalf("Propose returned an unexpected error: %v", err)
	}
	if got.Status != clank.StatusProposed {
		t.Errorf("a signal investigated to an evidence-backed decision should be proposed: want %s, got %s", clank.StatusProposed, got.Status)
	}
}

func newTestEngine(m clank.Model) clank.Engine {
	return clank.Engine{
		Model:     m,
		Store:     clank.NewMemStore(),
		Proposals: clank.NewMemProposalLog(),
		Gate:      clank.ReadinessGate{},
		Sink:      &recordingSink{},
		Tools:     []clank.Tool{fakeTool{name: "logs", digest: "503 rate 12%/min", ref: "loki:abc"}},
		MaxSteps:  5,
	}
}

func proposeArgs(t *testing.T, d clank.Decision) json.RawMessage {
	decision, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func TestPropose_WhenModelDeclines_YieldsNoAction(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "insufficient"}}},
	}}

	e := newTestEngine(model)

	got, err := e.Propose(context.Background(), testSignal())
	if err != nil {
		t.Fatalf("Propose returned an unexpected error: %v", err)
	}
	if got.Status != clank.StatusNoAction {
		t.Errorf("a signal investigated to an evidence-backed decision should be proposed: want %s, got %s", clank.StatusNoAction, got.Status)
	}
}
