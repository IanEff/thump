package step_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/step"
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
	key     string
	summary string
	live    bool
	subject string
}

func (f *fakeTool) Name() string { return "metrics" }

func (f *fakeTool) Spec() reason.ToolSpec {
	return reason.ToolSpec{
		Name:        "metrics",
		Description: "queries metrics",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (f *fakeTool) Run(_ context.Context, _ json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{
		Tool:    "metrics",
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

func TestRunHiss(t *testing.T) {
	t.Parallel()

	validProposal := filepath.Join("..", "..", "test", "fixtures", "proposals", "cart-failure.json")
	validPolicy := filepath.Join("..", "..", "config", "dev", "hiss", "policy.yaml")

	lowConfidenceProposal := filepath.Join(t.TempDir(), "low-confidence.json")
	lowConfSet := proposal.Set{
		Name:         "cart-burn-accel",
		SignalRef:    "slo_burn:cart",
		SLORef:       "cart-availability",
		FailureClass: "service_failure",
		ServiceTier:  "tier-1",
		Gate: &proposal.GateResult{
			BudgetOK:   true,
			DedupeOK:   true,
			EvidenceOK: true,
			Passed:     true,
		},
		Proposals: []proposal.Candidate{
			{
				ID:          "p1",
				ContractRef: "disable-cart-failure",
				Confidence:  0.50, // below 0.75 floor for tier-1 service_failure
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
		Recommended: "p1",
	}
	lowConfRaw, err := json.Marshal(lowConfSet)
	if err != nil {
		t.Fatalf("marshal low confidence proposal: %v", err)
	}
	if err := os.WriteFile(lowConfidenceProposal, lowConfRaw, 0o600); err != nil {
		t.Fatalf("write low confidence proposal: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		ctx          context.Context
		proposalFile string
		policyFile   string
		wantVerdict  decision.Verdict
		wantErr      bool
		wantTarget   error
	}{
		"RunHiss evaluates valid proposal and policy into approved governed decision": {
			ctx:          context.Background(),
			proposalFile: validProposal,
			policyFile:   validPolicy,
			wantVerdict:  decision.VerdictApproved,
		},
		"RunHiss escalates proposal when confidence is below policy floor": {
			ctx:          context.Background(),
			proposalFile: lowConfidenceProposal,
			policyFile:   validPolicy,
			wantVerdict:  decision.VerdictEscalate,
		},
		"RunHiss returns error for non-existent proposal file": {
			ctx:          context.Background(),
			proposalFile: "nonexistent.json",
			policyFile:   validPolicy,
			wantErr:      true,
		},
		"RunHiss returns error for non-existent policy file": {
			ctx:          context.Background(),
			proposalFile: validProposal,
			policyFile:   "nonexistent.yaml",
			wantErr:      true,
		},
		"RunHiss returns error when context is cancelled": {
			ctx:          canceledCtx,
			proposalFile: validProposal,
			policyFile:   validPolicy,
			wantErr:      true,
			wantTarget:   context.Canceled,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := step.RunHiss(tc.ctx, tc.proposalFile, tc.policyFile)
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
			if diff := cmp.Diff(tc.wantVerdict, got.Decision.Verdict); diff != "" {
				t.Errorf("wrong decision verdict (-want +got):\n%s", diff)
			}
			if got.Decision.Verdict == decision.VerdictApproved {
				if diff := cmp.Diff("slo_burn:cart", got.Decision.SignalRef); diff != "" {
					t.Errorf("wrong signalRef (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff("p1", got.Decision.CandidateRef); diff != "" {
					t.Errorf("wrong candidateRef (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(decision.BandActReversible, got.Decision.GrantedBand); diff != "" {
					t.Errorf("wrong grantedBand (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestRunThump(t *testing.T) {
	t.Parallel()

	validDecision := filepath.Join("..", "..", "test", "fixtures", "decisions", "cart-failure.json")
	validCatalog := filepath.Join("..", "..", "config", "dev", "actions", "catalog.yaml")

	escalateDecision := filepath.Join(t.TempDir(), "escalate-decision.json")
	escGoverned := decision.Governed{
		Decision: decision.Decision{
			ID:            "dec:slo_burn:cart:12345",
			ProposalRef:   "cart-burn-accel",
			SignalRef:     "slo_burn:cart",
			SLORef:        "cart-availability",
			CandidateRef:  "p1",
			Verdict:       decision.VerdictEscalate,
			Reasons:       []string{decision.ReasonConfidenceFloor},
			PolicyVersion: "v1",
			EvaluatedAt:   time.Now().UTC(),
		},
		Set: proposal.Set{
			Name:         "cart-burn-accel",
			SignalRef:    "slo_burn:cart",
			SLORef:       "cart-availability",
			FailureClass: "service_failure",
			Proposals: []proposal.Candidate{
				{
					ID:          "p1",
					ContractRef: "disable-cart-failure",
				},
			},
			Recommended: "p1",
		},
	}
	escRaw, err := json.Marshal(escGoverned)
	if err != nil {
		t.Fatalf("marshal escalate decision: %v", err)
	}
	if err := os.WriteFile(escalateDecision, escRaw, 0o600); err != nil {
		t.Fatalf("write escalate decision: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		ctx          context.Context
		decisionFile string
		catalogFile  string
		dryRun       bool
		wantMode     outcome.Mode
		wantResult   outcome.Result
		wantContract string
		wantErr      bool
		wantTarget   error
	}{
		"RunThump renders and dry-run executes approved governed decision": {
			ctx:          context.Background(),
			decisionFile: validDecision,
			catalogFile:  validCatalog,
			dryRun:       true,
			wantMode:     outcome.ModeDryRun,
			wantResult:   outcome.ResultRendered,
			wantContract: "disable-cart-failure",
		},
		"RunThump returns error when decision verdict is not approved": {
			ctx:          context.Background(),
			decisionFile: escalateDecision,
			catalogFile:  validCatalog,
			dryRun:       true,
			wantErr:      true,
			wantTarget:   thump.ErrUngoverned,
		},
		"RunThump returns error for non-existent decision file": {
			ctx:          context.Background(),
			decisionFile: "nonexistent.json",
			catalogFile:  validCatalog,
			dryRun:       true,
			wantErr:      true,
		},
		"RunThump returns error for non-existent catalog file": {
			ctx:          context.Background(),
			decisionFile: validDecision,
			catalogFile:  "nonexistent.yaml",
			dryRun:       true,
			wantErr:      true,
		},
		"RunThump returns error when context is cancelled": {
			ctx:          canceledCtx,
			decisionFile: validDecision,
			catalogFile:  validCatalog,
			dryRun:       true,
			wantErr:      true,
			wantTarget:   context.Canceled,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := step.RunThump(tc.ctx, tc.decisionFile, tc.catalogFile, tc.dryRun)
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
			if diff := cmp.Diff(tc.wantMode, got.Mode); diff != "" {
				t.Errorf("wrong outcome mode (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantResult, got.Result); diff != "" {
				t.Errorf("wrong outcome result (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantContract, got.ContractRef); diff != "" {
				t.Errorf("wrong contractRef (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("slo_burn:cart", got.SignalRef); diff != "" {
				t.Errorf("wrong signalRef (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunClank(t *testing.T) {
	t.Parallel()

	validDetection := filepath.Join("..", "..", "test", "fixtures", "detections", "cart-failure.json")
	validProfile := filepath.Join("..", "..", "config", "dev")

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		ctx           context.Context
		detectionFile string
		profileDir    string
		model         reason.Model
		tools         map[string]reason.Tool
		wantRec       string
		wantErr       bool
		wantTarget    error
	}{
		"RunClank reasons over cart failure detection with mock model and produces valid proposal set": {
			ctx:           context.Background(),
			detectionFile: validDetection,
			profileDir:    validProfile,
			model: &fakeModel{script: []reason.Completion{
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
								Confidence:  0.85,
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
			}},
			tools: map[string]reason.Tool{
				"metrics": &fakeTool{key: "cart_error_ratio", summary: "cart_error_ratio = 0.50", live: true, subject: "cart"},
			},
			wantRec: "p1",
		},
		"RunClank returns error when detection file does not exist": {
			ctx:           context.Background(),
			detectionFile: "nonexistent.json",
			profileDir:    validProfile,
			model:         &fakeModel{},
			wantErr:       true,
		},
		"RunClank returns error when profile directory does not exist": {
			ctx:           context.Background(),
			detectionFile: validDetection,
			profileDir:    "nonexistent-dir",
			model:         &fakeModel{},
			wantErr:       true,
		},
		"RunClank returns error when context is cancelled": {
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

			got, err := step.RunClankWithModelAndToolsForTest(tc.ctx, tc.detectionFile, tc.profileDir, tc.model, tc.tools)
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
			if got.Gate == nil || !got.Gate.Passed {
				t.Errorf("expected gate to pass, got %+v", got.Gate)
			}
			if diff := cmp.Diff(tc.wantRec, got.Recommended); diff != "" {
				t.Errorf("wrong recommended proposal (-want +got):\n%s", diff)
			}
			if len(got.Proposals) != 1 {
				t.Errorf("expected 1 proposal, got %d", len(got.Proposals))
			}
		})
	}
}

func TestRunClank_MissingAPIKey(t *testing.T) {
	validDetection := filepath.Join("..", "..", "test", "fixtures", "detections", "cart-failure.json")
	validProfile := filepath.Join("..", "..", "config", "dev")

	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := step.RunClank(context.Background(), validDetection, validProfile, "", "")
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
}

func TestRunRattle(t *testing.T) {
	t.Parallel()

	validWatch := filepath.Join("..", "..", "config", "dev", "rattle", "watch.yaml")
	validQuery := filepath.Join("..", "..", "config", "dev", "rattle", "query.yaml")

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := map[string]struct {
		ctx             context.Context
		watchFile       string
		queryConfigFile string
		serverCtor      func(t *testing.T) *httptest.Server
		promURLOverride *string
		wantService     string
		wantSLORef      string
		wantDetector    string
		wantErr         bool
		wantTarget      error
	}{
		"RunRattle detects accelerating burn rate from Prometheus response and emits signal detection": {
			ctx:             context.Background(),
			watchFile:       validWatch,
			queryConfigFile: validQuery,
			serverCtor: func(t *testing.T) *httptest.Server {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					now := time.Now().Unix()
					query := r.URL.Query().Get("query")
					if query == `slo:current_burn_rate:ratio{sloth_id="cart-availability"}` {
						resp := fmt.Sprintf(`{
							"status": "success",
							"data": {
								"resultType": "matrix",
								"result": [{
									"metric": {"__name__": "slo:current_burn_rate:ratio", "sloth_id": "cart-availability"},
									"values": [
										[%d, "1.0"],
										[%d, "2.0"],
										[%d, "4.0"],
										[%d, "8.0"]
									]
								}]
							}
						}`, now-30, now-20, now-10, now)
						_, _ = w.Write([]byte(resp))
						return
					}
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
				}))
				t.Cleanup(srv.Close)
				return srv
			},
			wantService:  "cart",
			wantSLORef:   "cart-availability",
			wantDetector: "burn_rate_acceleration",
		},
		"RunRattle returns ErrNoDetection when metrics do not breach acceleration threshold": {
			ctx:             context.Background(),
			watchFile:       validWatch,
			queryConfigFile: validQuery,
			serverCtor: func(t *testing.T) *httptest.Server {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					now := time.Now().Unix()
					query := r.URL.Query().Get("query")
					if query == `slo:current_burn_rate:ratio{sloth_id="cart-availability"}` {
						resp := fmt.Sprintf(`{
							"status": "success",
							"data": {
								"resultType": "matrix",
								"result": [{
									"metric": {"__name__": "slo:current_burn_rate:ratio", "sloth_id": "cart-availability"},
									"values": [
										[%d, "0.1"],
										[%d, "0.1"],
										[%d, "0.1"],
										[%d, "0.1"]
									]
								}]
							}
						}`, now-30, now-20, now-10, now)
						_, _ = w.Write([]byte(resp))
						return
					}
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
				}))
				t.Cleanup(srv.Close)
				return srv
			},
			wantErr:    true,
			wantTarget: step.ErrNoDetection,
		},
		"RunRattle returns error when watch file does not exist": {
			ctx:             context.Background(),
			watchFile:       "nonexistent.yaml",
			queryConfigFile: validQuery,
			serverCtor: func(t *testing.T) *httptest.Server {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
				}))
				t.Cleanup(srv.Close)
				return srv
			},
			wantErr: true,
		},
		"RunRattle returns error when query config file does not exist": {
			ctx:             context.Background(),
			watchFile:       validWatch,
			queryConfigFile: "nonexistent.yaml",
			serverCtor: func(t *testing.T) *httptest.Server {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
				}))
				t.Cleanup(srv.Close)
				return srv
			},
			wantErr: true,
		},
		"RunRattle returns error when promURL is empty": {
			ctx:             context.Background(),
			watchFile:       validWatch,
			queryConfigFile: validQuery,
			promURLOverride: stringPtr(""),
			wantErr:         true,
		},
		"RunRattle returns error when context is cancelled": {
			ctx:             canceledCtx,
			watchFile:       validWatch,
			queryConfigFile: validQuery,
			serverCtor: func(t *testing.T) *httptest.Server {
				t.Helper()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
				}))
				t.Cleanup(srv.Close)
				return srv
			},
			wantErr:    true,
			wantTarget: context.Canceled,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			promURL := ""
			if tc.promURLOverride != nil {
				promURL = *tc.promURLOverride
			} else if tc.serverCtor != nil {
				srv := tc.serverCtor(t)
				promURL = srv.URL
			}

			got, err := step.RunRattle(tc.ctx, tc.watchFile, tc.queryConfigFile, promURL)
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
			if diff := cmp.Diff(tc.wantService, got.OriginService); diff != "" {
				t.Errorf("wrong origin service (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantSLORef, got.SLORef); diff != "" {
				t.Errorf("wrong sloRef (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantDetector, got.DetectorType); diff != "" {
				t.Errorf("wrong detector type (-want +got):\n%s", diff)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
