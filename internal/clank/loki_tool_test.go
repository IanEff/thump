package clank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/subjects"
)

func TestLokiTool_Run(t *testing.T) {
	tests := map[string]struct {
		input          string
		lokiResponse   string
		lokiStatusCode int
		wantQuery      string
		wantRef        proposal.EvidenceRef
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
			wantRef: proposal.EvidenceRef{
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
			wantRef: proposal.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="rook-ceph"}`,
				Summary: "no matching log lines",
				Live:    false,
			},
		},
		"Run given a server error returns non-live evidence": {
			input:          `{"namespace": "rook-ceph"}`,
			lokiStatusCode: http.StatusInternalServerError,
			wantRef: proposal.EvidenceRef{
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
			wantRef: proposal.EvidenceRef{
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

// TestLokiTool_Run_StampsTheSubjectItsCoordinatesResolveTo pins the tag
// without which a log citation is inert: gate.go's coherentSubject fails
// closed on an untagged ref, so a loki ref with no Subject can never ground a
// proposal on its own and never counts toward the two-backend corroboration
// floor. The tag comes from authored rules and the query's own coordinates,
// never from the model.
func TestLokiTool_Run_StampsTheSubjectItsCoordinatesResolveTo(t *testing.T) {
	t.Parallel()

	index := subjects.SubjectIndex{
		{Subject: "ceph-osd", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"ceph_daemon_type": "osd"}}},
	}

	tests := map[string]struct {
		input        string
		lokiResponse string
		want         proposal.EvidenceRef
	}{
		"Run stamps the subject a matching rule names on a live citation": {
			input: `{"namespace": "rook-ceph", "labels": {"ceph_daemon_type": "osd"}}`,
			lokiResponse: `{"status":"success","data":{"resultType":"streams","result":[
				{"stream":{"namespace":"rook-ceph"},"values":[["1783535136846051765","osd is flapping"]]}]}}`,
			want: proposal.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="rook-ceph", ceph_daemon_type="osd"}`,
				Summary: "1 log line(s); last: osd is flapping",
				Ref:     "loki://rook-ceph/ceph_daemon_type=osd",
				Live:    true,
				Subject: "ceph-osd",
			},
		},
		"Run stamps the subject even when the query returned no lines": {
			input:        `{"namespace": "rook-ceph", "labels": {"ceph_daemon_type": "osd"}}`,
			lokiResponse: `{"status":"success","data":{"resultType":"streams","result":[]}}`,
			want: proposal.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="rook-ceph", ceph_daemon_type="osd"}`,
				Summary: "no matching log lines",
				Live:    false,
				Subject: "ceph-osd",
			},
		},
		"Run stamps no subject for coordinates no rule claims": {
			input: `{"namespace": "otel-demo"}`,
			lokiResponse: `{"status":"success","data":{"resultType":"streams","result":[
				{"stream":{"namespace":"otel-demo"},"values":[["1783535136846051765","cart timed out"]]}]}}`,
			want: proposal.EvidenceRef{
				Tool:    "loki",
				Query:   `{namespace="otel-demo"}`,
				Summary: "1 log line(s); last: cart timed out",
				Ref:     "loki://otel-demo",
				Live:    true,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.lokiResponse))
			}))
			defer ts.Close()

			tool := &clank.LokiTool{BaseURL: ts.URL, Subjects: index}
			got, err := tool.Run(t.Context(), json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong EvidenceRef for the query's coordinates (-want +got)\n", diff)
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
