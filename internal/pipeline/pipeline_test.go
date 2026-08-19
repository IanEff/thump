package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/pipeline"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/thump"
)

type fakeModel struct {
	script []reason.Completion
	calls  int
}

func (f *fakeModel) Complete(_ context.Context, _ []reason.Message, _ []reason.ToolSpec) (reason.Completion, error) {
	if f.calls >= len(f.script) {
		return reason.Completion{}, errors.New("fake model exhausted script")
	}
	resp := f.script[f.calls]
	f.calls++
	return resp, nil
}

type fakeTool struct {
	name    string
	key     string
	summary string
	live    bool
	subject string
}

func (f *fakeTool) Name() string {
	if f.name != "" {
		return f.name
	}
	return "metrics"
}

func (f *fakeTool) Spec() reason.ToolSpec {
	return reason.ToolSpec{
		Name:        f.Name(),
		Description: "queries " + f.Name(),
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (f *fakeTool) Run(_ context.Context, _ json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{
		Tool:    f.Name(),
		Key:     f.key,
		Summary: f.summary,
		Live:    f.live,
		Subject: f.subject,
	}, nil
}

func proposeArgs(t *testing.T, set proposal.Set) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}
	return raw
}

func TestRun(t *testing.T) {
	t.Parallel()

	validDetection := filepath.Join("..", "..", "test", "fixtures", "detections", "cart-failure.json")
	validProfile := filepath.Join("..", "..", "config", "dev")

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	validModel := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{
			Name: "metrics",
			Args: json.RawMessage(`{"q":"cart_error_ratio"}`),
		}}},
		{ToolCalls: []reason.ToolCall{{
			Name: "loki",
			Args: json.RawMessage(`{"q":"cart_errors"}`),
		}}},
		{ToolCalls: []reason.ToolCall{{
			Name: "propose",
			Args: proposeArgs(t, proposal.Set{
				FailureClass: "service_failure",
				Hypotheses: []proposal.Hypothesis{
					{Name: "cart-service-failure", Weight: 0.9},
				},
				Proposals: []proposal.Candidate{
					{
						ID:          "p1",
						ContractRef: "disable-cart-failure",
						Confidence:  0.85,
						Citations:   []string{"cart_error_ratio", "loki_cart_errors"},
						GovernanceLevel: &proposal.GovernanceLevel{
							Band: "act_reversible",
						},
						ReversalPath: &proposal.ReversalPath{
							Method:    "enable-cart-failure",
							Watching:  "cart_error_ratio",
							Trigger:   "cart_error_ratio < 0.02",
							Automatic: true,
						},
					},
				},
			}),
		}}},
	}}

	lowConfidenceModel := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{
			Name: "metrics",
			Args: json.RawMessage(`{"q":"cart_error_ratio"}`),
		}}},
		{ToolCalls: []reason.ToolCall{{
			Name: "propose",
			Args: proposeArgs(t, proposal.Set{
				FailureClass: "service_failure",
				Hypotheses: []proposal.Hypothesis{
					{Name: "cart-service-failure", Weight: 0.9},
				},
				Proposals: []proposal.Candidate{
					{
						ID:          "p1",
						ContractRef: "disable-cart-failure",
						Confidence:  0.50, // below 0.75 floor for tier-1 service_failure
						Citations:   []string{"cart_error_ratio"},
						GovernanceLevel: &proposal.GovernanceLevel{
							Band: "act_reversible",
						},
						ReversalPath: &proposal.ReversalPath{
							Method:    "enable-cart-failure",
							Watching:  "cart_error_ratio",
							Trigger:   "cart_error_ratio < 0.02",
							Automatic: true,
						},
					},
				},
			}),
		}}},
	}}

	validTools := map[string]reason.Tool{
		"metrics": &fakeTool{name: "metrics", key: "cart_error_ratio", summary: "cart_error_ratio = 0.50", live: true, subject: "cart"},
		"loki":    &fakeTool{name: "loki", key: "loki_cart_errors", summary: "50 log lines with error", live: true, subject: "cart"},
	}

	tests := map[string]struct {
		ctx           context.Context
		detectionFile string
		profileDir    string
		model         reason.Model
		tools         map[string]reason.Tool
		wantService   string
		wantRec       string
		wantVerdict   decision.Verdict
		wantMode      outcome.Mode
		wantResult    outcome.Result
		wantContract  string
		wantErr       bool
		wantTarget    error
	}{
		"RunWithModelForTest executes cart failure simulation end-to-end and yields rendered dry-run outcome": {
			ctx:           context.Background(),
			detectionFile: validDetection,
			profileDir:    validProfile,
			model:         validModel,
			tools:         validTools,
			wantService:   "cart",
			wantRec:       "p1",
			wantVerdict:   decision.VerdictApproved,
			wantMode:      outcome.ModeDryRun,
			wantResult:    outcome.ResultRendered,
			wantContract:  "disable-cart-failure",
		},
		"RunWithModelForTest returns error when policy evaluation rejects or escalates decision": {
			ctx:           context.Background(),
			detectionFile: validDetection,
			profileDir:    validProfile,
			model:         lowConfidenceModel,
			tools:         validTools,
			wantErr:       true,
			wantTarget:    thump.ErrUngoverned,
		},
		"RunWithModelForTest returns error when detection file does not exist": {
			ctx:           context.Background(),
			detectionFile: "nonexistent.json",
			profileDir:    validProfile,
			model:         &fakeModel{},
			wantErr:       true,
		},
		"RunWithModelForTest returns error when profile directory does not exist": {
			ctx:           context.Background(),
			detectionFile: validDetection,
			profileDir:    "nonexistent-dir",
			model:         &fakeModel{},
			wantErr:       true,
		},
		"RunWithModelForTest returns error when context is cancelled": {
			ctx:           canceledCtx,
			detectionFile: validDetection,
			profileDir:    validProfile,
			model:         &fakeModel{},
			wantErr:       true,
			wantTarget:    context.Canceled,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := pipeline.RunWithModelForTest(tc.ctx, tc.detectionFile, tc.profileDir, tc.model, tc.tools)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.wantTarget != nil && !errors.Is(err, tc.wantTarget) {
					t.Fatalf("expected error wrapping %v, got %v", tc.wantTarget, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantService, got.Detection.OriginService); diff != "" {
				t.Errorf("wrong detection origin service (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("slo_burn:cart", got.Detection.Fingerprint); diff != "" {
				t.Errorf("wrong detection fingerprint (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantRec, got.Proposal.Recommended); diff != "" {
				t.Errorf("wrong proposal recommended (-want +got):\n%s", diff)
			}
			if got.Proposal.Gate == nil || !got.Proposal.Gate.Passed {
				t.Errorf("expected proposal gate to pass, got %+v", got.Proposal.Gate)
			}
			if diff := cmp.Diff(tc.wantVerdict, got.Decision.Decision.Verdict); diff != "" {
				t.Errorf("wrong decision verdict (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("slo_burn:cart", got.Decision.Decision.SignalRef); diff != "" {
				t.Errorf("wrong decision signalRef (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantRec, got.Decision.Decision.CandidateRef); diff != "" {
				t.Errorf("wrong decision candidateRef (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(decision.BandActReversible, got.Decision.Decision.GrantedBand); diff != "" {
				t.Errorf("wrong decision grantedBand (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantMode, got.Outcome.Mode); diff != "" {
				t.Errorf("wrong outcome mode (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantResult, got.Outcome.Result); diff != "" {
				t.Errorf("wrong outcome result (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantContract, got.Outcome.ContractRef); diff != "" {
				t.Errorf("wrong outcome contractRef (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("slo_burn:cart", got.Outcome.SignalRef); diff != "" {
				t.Errorf("wrong outcome signalRef (-want +got):\n%s", diff)
			}
			if got.Duration <= 0 {
				t.Errorf("expected positive duration, got %v", got.Duration)
			}
		})
	}
}

func TestRun_MissingAPIKey(t *testing.T) {
	validDetection := filepath.Join("..", "..", "test", "fixtures", "detections", "cart-failure.json")
	validProfile := filepath.Join("..", "..", "config", "dev")

	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := pipeline.Run(context.Background(), validDetection, validProfile, "", "")
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
}
