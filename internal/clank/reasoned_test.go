package clank_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

// TestPropose_LogsReasonedOnLLMCompleteFailure pins the actual 2026-07-14
// incident: every Model.Complete call fails (a DNS outage), Propose returns
// on the very first attempt (the reason loop has no internal retry), and
// before this fix that failure produced zero terminal log output — only
// beat.Stage's generic per-call "llm_complete" line, with no run identity.
// That gap is what made the incident require gopls, not kubectl logs, to
// diagnose.
func TestPropose_LogsReasonedOnLLMCompleteFailure(t *testing.T) {
	getLogs := captureLog(t)
	model := &fakeModel{err: errors.New("dial tcp: lookup api.anthropic.com: no such host")}
	e, _ := newTestEngine(model)

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err == nil {
		t.Fatal("a Model.Complete failure must halt Propose with an error")
	}

	reasoned := onlyReasonedLine(t, getLogs())
	if reasoned["level"] != "ERROR" {
		t.Errorf("a failed run's reasoned line must log at ERROR, got %+v", reasoned)
	}
	if reasoned["fingerprint"] != "fp-checkout-latency-001" {
		t.Errorf("reasoned line missing/wrong fingerprint: %+v", reasoned)
	}
	if s, _ := reasoned["run_id"].(string); s == "" {
		t.Errorf("reasoned line missing run_id: %+v", reasoned)
	}
	if s, _ := reasoned["err"].(string); !strings.Contains(s, "no such host") {
		t.Errorf("reasoned line must carry the real failure, got %+v", reasoned)
	}
}

// TestPropose_LogsReasonedOnCheckpointFailure proves the terminal line
// isn't special-cased to the llm_complete path — Store.Checkpoint failing
// (a structurally different mid-loop error) gets the same treatment.
func TestPropose_LogsReasonedOnCheckpointFailure(t *testing.T) {
	getLogs := captureLog(t)
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
	}}
	e, _ := newTestEngine(model)
	e.Store = &failingStore{MemStore: clank.NewMemStore(), failOn: 0}

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err == nil {
		t.Fatal("a checkpoint failure must halt Propose with an error")
	}

	reasoned := onlyReasonedLine(t, getLogs())
	if reasoned["level"] != "ERROR" {
		t.Errorf("want ERROR, got %+v", reasoned)
	}
	if s, _ := reasoned["err"].(string); !strings.Contains(s, "disk on fire") {
		t.Errorf("reasoned line must carry the checkpoint failure, got %+v", reasoned)
	}
}

// TestPropose_LogsReasonedOnSuccess pins the other half: the line must also
// fire — exactly once — on the happy path, carrying phase, and with no err
// field. This guards against an implementation that only adds error-path
// logging and leaves the old caller-side success logging in place, which
// would double-log every successful run.
func TestPropose_LogsReasonedOnSuccess(t *testing.T) {
	getLogs := captureLog(t)
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}
	e, _ := newTestEngine(model)

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	reasoned := onlyReasonedLine(t, getLogs())
	if reasoned["level"] != "INFO" {
		t.Errorf("a successful run's reasoned line must log at INFO, got %+v", reasoned)
	}
	if diff := cmp.Diff(got.Status.Phase, reasoned["phase"]); diff != "" {
		t.Error("reasoned.phase must match the returned Set's phase (-want +got)\n", diff)
	}
	if _, has := reasoned["err"]; has {
		t.Errorf("a successful run's reasoned line must not carry an err field: %+v", reasoned)
	}
	if diff := cmp.Diff("throttle-non-critical-paths", reasoned["contractRef"]); diff != "" {
		t.Error("reasoned line must carry the recommended candidate's ContractRef, not just its ID (-want +got)\n", diff)
	}
}
