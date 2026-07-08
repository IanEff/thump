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

func TestLokiTool_Run(t *testing.T) {
	tests := map[string]struct {
		input          string
		lokiResponse   string
		lokiStatusCode int
		wantQuery      string
		wantRef        clank.EvidenceRef
	}{
		"Run given matching streams returns live evidence": {
			input:          `{"namespace": "rook-ceph", "labels": {"ceph_daemon_type": "mon"}}`,
			lokiStatusCode: http.StatusOK,
			lokiResponse: `{
				"status": "success",
				"data": {
					"resultType": "streams",
					"result": [
						{"stream": {"namespace": "rook-ceph", "ceph_daemon_type": "mon"},
						 "values": [["1783535136846051765", "mon is slow to respond"]]}
					]
				}
			}`,
			wantQuery: `{namespace="rook-ceph", ceph_daemon_type="mon"}`,
			wantRef: clank.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="rook-ceph", ceph_daemon_type="mon"}`,
				Summary: "1 log line(s); last: mon is slow to respond",
				Ref:     "loki://rook-ceph/ceph_daemon_type=mon",
				Live:    true,
			},
		},
		"Run given no matching streams returns non-live evidence": {
			input:          `{"namespace": "rook-ceph"}`,
			lokiStatusCode: http.StatusOK,
			lokiResponse: `{
				"status": "success",
				"data": {"resultType": "streams", "result": []}
			}`,
			wantRef: clank.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="rook-ceph"}`,
				Summary: "no matching log lines",
				Live:    false,
			},
		},
		"Run given a server error returns non-live evidence": {
			input:          `{"namespace": "rook-ceph"}`,
			lokiStatusCode: http.StatusInternalServerError,
			wantRef: clank.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="rook-ceph"}`,
				Summary: "loki returned status: 500 Internal Server Error",
				Live:    false,
			},
		},
		"Run quotes an attempted LogQL injection in the query filter": {
			input:          `{"namespace": "rook-ceph", "query": "\"} or {namespace=~\".*\""}`,
			lokiStatusCode: http.StatusOK,
			lokiResponse:   `{"status": "success", "data": {"resultType": "streams", "result": []}}`,
			wantRef: clank.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="rook-ceph"} |= "\"} or {namespace=~\".*\""`,
				Summary: "no matching log lines",
				Live:    false,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Path; got != "/loki/api/v1/query_range" {
					t.Errorf("unexpected path: %s", got)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.lokiStatusCode)
				_, _ = w.Write([]byte(tc.lokiResponse))
			}))
			defer ts.Close()

			tool := &clank.LokiTool{BaseURL: ts.URL}

			gotRef, err := tool.Run(context.Background(), json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.wantRef, gotRef); diff != "" {
				t.Error("LokiTool.Run returned wrong EvidenceRef", diff)
			}
		})
	}
}

func TestLokiTool_RunGivenUndecodableArgsReturnsError(t *testing.T) {
	tool := &clank.LokiTool{BaseURL: "http://example.invalid"}

	_, err := tool.Run(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
