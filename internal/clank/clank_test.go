package clank_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/outcome"
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

func TestRunLoop_ReturnsPromptlyWhenContextIsCancelled(t *testing.T) {
	t.Parallel()
	outbox := t.TempDir()
	tr := &clank.Transport{Inbox: t.TempDir(), Engine: newProposingEngine(t, outbox)}
	re := &clank.ReturnEdge{
		Inbox: t.TempDir(),
		Click: clank.Click{Ledger: clank.NewMemProposalLog(), Cases: clank.NewCaseBase()},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before runLoop even starts — the ticker (5s) must never win this race

	done := make(chan struct{})
	go func() {
		clank.RunLoopForTest(ctx, tr, re)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLoop did not return promptly after its context was cancelled")
	}
}

func TestNextDelay_GrowsCapsAndResets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cur  time.Duration
		ok   bool
		want time.Duration
	}{
		{"first failure doubles from current", 5 * time.Second, false, 10 * time.Second},
		{"caps instead of overshooting", 4 * time.Minute, false, 5 * time.Minute},
		{"already at cap stays at cap", 5 * time.Minute, false, 5 * time.Minute},
		{"success snaps back to base regardless of cur", 3 * time.Minute, true, 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := clank.NextDelayForTest(tt.cur, tt.ok)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Error("nextDelay (-want +got)\n", diff)
			}
		})
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
	model := &fakeModel{script: []clank.Completion{
		// turn 1: gather live evidence — required for the gate's evidence floor.
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		// turn 2: propose a catalogued action.
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Hypotheses:   []clank.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}
	tools := map[string]clank.Tool{"metrics": metricsTool{}}
	intake := clank.NewIntake(
		fakeTopo{snap: clank.TopologySnapshot{
			Downstream: []clank.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
		}},
		fakeChange{snap: clank.ChangeSnapshot{Events: []clank.ChangeEvent{
			{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
		}}},
	)
	cat := clank.NewStaticCatalog([]clank.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []clank.FailureClass{clank.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
	}})

	l := clank.NewLoopForTest(model, tools, intake, cat, t.TempDir(), t.TempDir())
	return testLoop{Engine: l.Engine, ReturnEdge: l.ReturnEdge, Cases: l.Cases, OutcomeInbox: l.OutcomeInbox}
}

// writeOutcomeFor drops a live-success Outcome answering the given set into
// dir, threading the SignalRef and a candidate's ContractRef through — the
// fields ReturnEdge.Tick / MemProposalLog.Observe actually match on.
func writeOutcomeFor(t *testing.T, dir string, set clank.ProposalSet) {
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
