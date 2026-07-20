package clank_test

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
	"github.com/ianeff/thump/internal/clank"
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
		subjects    map[string]string
		wantSubject string
	}{
		"Run stamps the configured Subject for a tagged query": {
			query:       "product_catalog_error_ratio",
			subjects:    map[string]string{"product_catalog_error_ratio": "product-catalog"},
			wantSubject: "product-catalog",
		},
		"Run stamps no Subject for a query absent from Subjects": {
			query:       "ceph_health",
			subjects:    map[string]string{"product_catalog_error_ratio": "product-catalog"},
			wantSubject: "",
		},
		"Run stamps no Subject when Subjects is nil": {
			query:       "ceph_health",
			subjects:    nil,
			wantSubject: "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tool := &clank.MetricsTool{
				BaseURL:  ts.URL,
				Queries:  map[string]string{tc.query: "irrelevant_promql"},
				Subjects: tc.subjects,
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

// TestLoadEvidenceQueries_ParsesSubjectTags pins the on-disk contract:
// evidence-queries.yaml's optional subject: field must land in the second
// return value, keyed by query name, and a query with no subject: line must
// be absent from it entirely — not present with an empty string — so
// MetricsTool.Subjects[name] misses cleanly via the zero value.
func TestLoadEvidenceQueries_ParsesSubjectTags(t *testing.T) {
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

	queries, subjects, err := clank.LoadEvidenceQueries(path)
	if err != nil {
		t.Fatalf("LoadEvidenceQueries errored: %v", err)
	}

	wantQueries := map[string]string{
		"argocd_apps_out_of_sync": `count(argocd_app_info{sync_status!="Synced"}) or vector(0)`,
		"ceph_health":             `ceph_health_status{job="rook-ceph-mgr"}`,
	}
	if diff := cmp.Diff(wantQueries, queries); diff != "" {
		t.Error("wrong queries map", diff)
	}

	wantSubjects := map[string]string{"argocd_apps_out_of_sync": "argocd"}
	if diff := cmp.Diff(wantSubjects, subjects); diff != "" {
		t.Error("wrong subjects map (untagged queries must be absent, not empty-string)", diff)
	}
}
