package evidence_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/subjects"
)

func TestMetricsTool_Run(t *testing.T) {
	tests := map[string]struct {
		input          string
		promResponse   string
		promStatusCode int
		wantRef        proposal.EvidenceRef
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
			wantRef: proposal.EvidenceRef{
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
			wantRef: proposal.EvidenceRef{
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
			wantRef: proposal.EvidenceRef{
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
			wantRef: proposal.EvidenceRef{
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
			tool := &evidence.MetricsTool{
				BaseURL: ts.URL,
				Queries: map[string]evidence.Query{
					"ceph_health": {Query: "ceph_health_status"},
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

// TestMetricsTool_RunStampsSubject pins the plumbing the gate's topology
// coherence check depends on: Run must copy the query's configured Subject
// onto the returned EvidenceRef, and a query with no Subjects entry must
// stamp none — the zero-blast-radius default that keeps every untagged
// query behaving exactly as it did before Subject existed.
func TestMetricsTool_RunStampsSubject(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"__name__":"x"},"value":[1688745600,"1"]}
		]}}`))
	}))
	defer ts.Close()

	cases := map[string]struct {
		query       string
		subject     string
		wantSubject string
	}{
		"Run stamps the configured Subject for a tagged query": {
			query:       "product_catalog_error_ratio",
			subject:     "product-catalog",
			wantSubject: "product-catalog",
		},
		"Run stamps no Subject for an untagged query": {
			query:       "ceph_health",
			subject:     "",
			wantSubject: "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tool := &evidence.MetricsTool{
				BaseURL: ts.URL,
				Queries: map[string]evidence.Query{tc.query: {Query: "irrelevant_promql", Subject: tc.subject}},
			}
			ref, err := tool.Run(context.Background(), json.RawMessage(`{"q":"`+tc.query+`"}`))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantSubject, ref.Subject); diff != "" {
				t.Error("wrong Subject on the returned EvidenceRef", diff)
			}
		})
	}
}

// TestLoadEvidenceConfig_ParsesSubjectTags pins the on-disk contract:
// evidence-queries.yaml's optional subject: field must land in the query's
// Query.Subject — a query with no subject: line gets the zero value,
// not a fabricated one, so MetricsTool stamps no Subject for it.
func TestLoadEvidenceConfig_ParsesSubjectTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evidence-queries.yaml")
	const doc = `
version: v1
queries:
  - name: argocd_apps_out_of_sync
    query: count(argocd_app_info{sync_status!="Synced"}) or vector(0)
    subject: argocd
  - name: ceph_health
    query: ceph_health_status{job="rook-ceph-mgr"}
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ev, err := evidence.LoadEvidenceConfig(path)
	if err != nil {
		t.Fatalf("LoadEvidenceConfig errored: %v", err)
	}

	wantQueries := map[string]evidence.Query{
		"argocd_apps_out_of_sync": {
			Name:    "argocd_apps_out_of_sync",
			Query:   `count(argocd_app_info{sync_status!="Synced"}) or vector(0)`,
			Subject: "argocd",
		},
		"ceph_health": {
			Name:  "ceph_health",
			Query: `ceph_health_status{job="rook-ceph-mgr"}`,
		},
	}
	if diff := cmp.Diff(wantQueries, ev.Queries); diff != "" {
		t.Error("wrong queries map", diff)
	}
}

// TestLoadEvidenceConfig_ParsesSubjectRules pins the other half of the file's
// contract: the subjects: block the log and cluster tools resolve through.
// Its absence must yield an empty index, not an error — a rig running metrics
// alone is a supported deployment, it just can't corroborate across backends.
func TestLoadEvidenceConfig_ParsesSubjectRules(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		doc  string
		want subjects.SubjectIndex
	}{
		"LoadEvidenceConfig parses a subjects block into rules in file order": {
			doc: `
version: v1
queries:
  - name: ceph_health
    query: ceph_health_status
subjects:
  - subject: ceph-osd
    namespace: rook-ceph
    labels:
      app: rook-ceph-osd
  - subject: acme-api
    namespace: acme
`,
			want: subjects.SubjectIndex{
				{Subject: "ceph-osd", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}}},
				{Subject: "acme-api", Coordinates: subjects.Coordinates{Namespace: "acme"}},
			},
		},
		"LoadEvidenceConfig yields an empty index for a file with no subjects block": {
			doc: `
version: v1
queries:
  - name: ceph_health
    query: ceph_health_status
`,
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "evidence-queries.yaml")
			if err := os.WriteFile(path, []byte(tc.doc), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			ev, err := evidence.LoadEvidenceConfig(path)
			if err != nil {
				t.Fatalf("LoadEvidenceConfig errored: %v", err)
			}
			if diff := cmp.Diff(tc.want, ev.Index); diff != "" {
				t.Error("wrong subject rules parsed from the file (-want +got)\n", diff)
			}
		})
	}
}
