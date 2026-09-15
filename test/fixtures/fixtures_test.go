package fixtures_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

type promResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func TestFixtures_SchemaValidity(t *testing.T) {
	t.Parallel()

	t.Run("Detection fixtures unmarshal into valid signal.Detection structs", func(t *testing.T) {
		t.Parallel()

		tests := map[string]struct {
			relPath         string
			wantFingerprint string
			wantService     string
			wantSLORef      string
			wantMetric      string
		}{
			"UnmarshalDetection unmarshals cart-failure JSON into a valid signal.Detection": {
				relPath:         filepath.Join("detections", "cart-failure.json"),
				wantFingerprint: "slo_burn:cart",
				wantService:     "cart",
				wantSLORef:      "cart-availability",
				wantMetric:      "cart_error_ratio",
			},
		}

		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				data, err := os.ReadFile(tc.relPath) //nolint:gosec // G304: fixed testdata path, not user input
				if err != nil {
					t.Fatalf("failed to read fixture file %s: %v", tc.relPath, err)
				}

				var det signal.Detection
				if err := json.Unmarshal(data, &det); err != nil {
					t.Fatalf("failed to unmarshal signal.Detection from %s: %v", tc.relPath, err)
				}

				if diff := cmp.Diff(tc.wantFingerprint, det.Fingerprint); diff != "" {
					t.Errorf("wrong detection fingerprint (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantService, det.OriginService); diff != "" {
					t.Errorf("wrong detection origin service (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantSLORef, det.SLORef); diff != "" {
					t.Errorf("wrong detection sloRef (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantMetric, det.Divergence.Metric); diff != "" {
					t.Errorf("wrong detection divergence metric (-want +got):\n%s", diff)
				}
			})
		}
	})

	t.Run("Proposal fixtures unmarshal into valid proposal.Set structs", func(t *testing.T) {
		t.Parallel()

		tests := map[string]struct {
			relPath         string
			wantSignalRef   string
			wantRecommended string
			wantClass       proposal.FailureClass
			wantContractRef string
			wantGatePassed  bool
		}{
			"UnmarshalProposal unmarshals cart-failure JSON into a valid proposal.Set": {
				relPath:         filepath.Join("proposals", "cart-failure.json"),
				wantSignalRef:   "slo_burn:cart",
				wantRecommended: "p1",
				wantClass:       proposal.ClassServiceFailure,
				wantContractRef: "disable-cart-failure",
				wantGatePassed:  true,
			},
		}

		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				data, err := os.ReadFile(tc.relPath) //nolint:gosec // G304: fixed testdata path, not user input
				if err != nil {
					t.Fatalf("failed to read fixture file %s: %v", tc.relPath, err)
				}

				var set proposal.Set
				if err := json.Unmarshal(data, &set); err != nil {
					t.Fatalf("failed to unmarshal proposal.Set from %s: %v", tc.relPath, err)
				}

				if diff := cmp.Diff(tc.wantSignalRef, set.SignalRef); diff != "" {
					t.Errorf("wrong proposal SignalRef (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantRecommended, set.Recommended); diff != "" {
					t.Errorf("wrong proposal Recommended candidate (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantClass, set.FailureClass); diff != "" {
					t.Errorf("wrong proposal FailureClass (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantContractRef, set.ContractRefFor(set.Recommended)); diff != "" {
					t.Errorf("wrong candidate ContractRef (-want +got):\n%s", diff)
				}
				if set.Gate == nil {
					t.Fatal("want non-nil Gate on proposal.Set, got nil")
				}
				if diff := cmp.Diff(tc.wantGatePassed, set.Gate.Passed); diff != "" {
					t.Errorf("wrong gate passed status (-want +got):\n%s", diff)
				}
			})
		}
	})

	t.Run("Decision fixtures unmarshal into valid decision.Governed envelopes", func(t *testing.T) {
		t.Parallel()

		tests := map[string]struct {
			relPath       string
			wantSignalRef string
			wantVerdict   decision.Verdict
			wantBand      decision.Band
			wantPolicyVer string
		}{
			"UnmarshalDecision unmarshals cart-failure JSON into an auditable decision.Governed": {
				relPath:       filepath.Join("decisions", "cart-failure.json"),
				wantSignalRef: "slo_burn:cart",
				wantVerdict:   decision.VerdictApproved,
				wantBand:      decision.BandActReversible,
				wantPolicyVer: "v1",
			},
		}

		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				data, err := os.ReadFile(tc.relPath) //nolint:gosec // G304: fixed testdata path, not user input
				if err != nil {
					t.Fatalf("failed to read fixture file %s: %v", tc.relPath, err)
				}

				var gov decision.Governed
				if err := json.Unmarshal(data, &gov); err != nil {
					t.Fatalf("failed to unmarshal decision.Governed from %s: %v", tc.relPath, err)
				}

				if err := gov.Decision.Auditable(); err != nil {
					t.Fatalf("decision inside governed envelope fails Auditable invariant: %v", err)
				}

				if diff := cmp.Diff(tc.wantSignalRef, gov.Decision.SignalRef); diff != "" {
					t.Errorf("wrong decision SignalRef (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantVerdict, gov.Decision.Verdict); diff != "" {
					t.Errorf("wrong decision Verdict (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantBand, gov.Decision.GrantedBand); diff != "" {
					t.Errorf("wrong decision GrantedBand (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantPolicyVer, gov.Decision.PolicyVersion); diff != "" {
					t.Errorf("wrong decision PolicyVersion (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(tc.wantSignalRef, gov.Set.SignalRef); diff != "" {
					t.Errorf("wrong set SignalRef in governed envelope (-want +got):\n%s", diff)
				}
			})
		}
	})

	t.Run("Telemetry fixtures unmarshal into valid metric and stream responses", func(t *testing.T) {
		t.Parallel()

		tests := map[string]struct {
			relPath        string
			isProm         bool
			wantStatus     string
			wantResultType string
			wantAppName    string
		}{
			"UnmarshalPrometheusTelemetry unmarshals prom-cart-failure JSON into valid vector response": {
				relPath:        filepath.Join("telemetry", "prom-cart-failure.json"),
				isProm:         true,
				wantStatus:     "success",
				wantResultType: "vector",
				wantAppName:    "cart",
			},
			"UnmarshalLokiTelemetry unmarshals loki-cart-failure JSON into valid streams response": {
				relPath:        filepath.Join("telemetry", "loki-cart-failure.json"),
				isProm:         false,
				wantStatus:     "success",
				wantResultType: "streams",
				wantAppName:    "cartservice",
			},
		}

		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				data, err := os.ReadFile(tc.relPath) //nolint:gosec // G304: fixed testdata path, not user input
				if err != nil {
					t.Fatalf("failed to read fixture file %s: %v", tc.relPath, err)
				}

				if tc.isProm {
					var prom promResponse
					if err := json.Unmarshal(data, &prom); err != nil {
						t.Fatalf("failed to unmarshal Prometheus response from %s: %v", tc.relPath, err)
					}
					if diff := cmp.Diff(tc.wantStatus, prom.Status); diff != "" {
						t.Errorf("wrong prom status (-want +got):\n%s", diff)
					}
					if diff := cmp.Diff(tc.wantResultType, prom.Data.ResultType); diff != "" {
						t.Errorf("wrong prom resultType (-want +got):\n%s", diff)
					}
					if len(prom.Data.Result) == 0 {
						t.Fatal("want non-empty prom results, got empty")
					}
					if diff := cmp.Diff(tc.wantAppName, prom.Data.Result[0].Metric["app"]); diff != "" {
						t.Errorf("wrong prom metric app label (-want +got):\n%s", diff)
					}
				} else {
					var loki lokiResponse
					if err := json.Unmarshal(data, &loki); err != nil {
						t.Fatalf("failed to unmarshal Loki response from %s: %v", tc.relPath, err)
					}
					if diff := cmp.Diff(tc.wantStatus, loki.Status); diff != "" {
						t.Errorf("wrong loki status (-want +got):\n%s", diff)
					}
					if diff := cmp.Diff(tc.wantResultType, loki.Data.ResultType); diff != "" {
						t.Errorf("wrong loki resultType (-want +got):\n%s", diff)
					}
					if len(loki.Data.Result) == 0 {
						t.Fatal("want non-empty loki results, got empty")
					}
					if diff := cmp.Diff(tc.wantAppName, loki.Data.Result[0].Stream["app"]); diff != "" {
						t.Errorf("wrong loki stream app label (-want +got):\n%s", diff)
					}
				}
			})
		}
	})
}
