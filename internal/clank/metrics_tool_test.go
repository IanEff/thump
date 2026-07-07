package clank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
)

func TestMetricsTool_Run(t *testing.T) {
	tests := map[string]struct {
		input          string
		promResponse   string
		promStatusCode int
		wantRef        clank.EvidenceRef
		wantErr        bool
	}{
		"Run given a valid query returns live evidence": {
			input:          `{"q": "ceph_health"}`,
			promStatusCode: http.StatusOK,
			promResponse: `{
				"status": "success",
				"data": {
					"resultType": "vector",
					"result": [
						{
							"metric": {"__name__": "ceph_health_status"},
							"value": [1688745600, "1"]
						}
					]
				}
			}`,
			wantRef: clank.EvidenceRef{
				Tool:    "metrics",
				Query:   "ceph_health",
				Summary: "ceph_health = 1",
				Ref:     "metrics://ceph_health",
				Live:    true,
			},
		},
		"Run given an empty Prometheus result returns non-live evidence": {
			// This pins the "honesty" rule: no data means no live citation
			input:          `{"q": "ceph_health"}`,
			promStatusCode: http.StatusOK,
			promResponse: `{
				"status": "success",
				"data": {
					"resultType": "vector",
					"result": []
				}
			}`,
			wantRef: clank.EvidenceRef{
				Tool:    "metrics",
				Query:   "ceph_health",
				Summary: "query returned no data",
				Live:    false,
			},
		},
		"Run given an unknown query returns non-live evidence": {
			// Tests the map lookup failure
			input:          `{"q": "made_up_metric"}`,
			promStatusCode: http.StatusOK, // Server shouldn't even be hit, but safe to set
			wantRef: clank.EvidenceRef{
				Tool:    "metrics",
				Query:   "made_up_metric",
				Summary: "no such evidence query: made_up_metric",
				Live:    false,
			},
		},
		"Run given a server error returns non-live evidence": {
			// Tests that network/HTTP errors fail gracefully (Live: false) rather than crashing
			input:          `{"q": "ceph_health"}`,
			promStatusCode: http.StatusInternalServerError,
			wantRef: clank.EvidenceRef{
				Tool:    "metrics",
				Query:   "ceph_health",
				Summary: "prometheus returned status: 500 Internal Server Error",
				Live:    false,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// 1. Stand up a fake Prometheus server for this specific test case
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.promStatusCode)
				_, _ = w.Write([]byte(tc.promResponse))
			}))
			defer ts.Close()

			// 2. Setup the tool pointing to the fake server
			tool := &clank.MetricsTool{
				BaseURL: ts.URL,
				Queries: map[string]string{
					"ceph_health": "ceph_health_status",
				},
			}

			// 3. Action
			gotRef, err := tool.Run(context.Background(), json.RawMessage(tc.input))

			// 4. Assertions (Error Testing is Mandatory!)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// 5. cmp.Diff (want, got) - The Arundel Standard
			if diff := cmp.Diff(tc.wantRef, gotRef); diff != "" {
				t.Error("MetricsTool.Run returned wrong EvidenceRef", diff)
			}
		})
	}
}
