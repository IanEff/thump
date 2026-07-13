//go:build eval

package clank

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/contract"
	"sigs.k8s.io/yaml"
)

// evalCase is one row of the eval table: a committed fixture and the
// disposition a healthy reasoner should reach against the PRODUCTION
// catalog (contract.Default(), the same one Main wires). Unlike the
// golden-path suite (Stage 4, a scripted model), this drives the REAL
// Model — it's a score, not a proof, and it never runs in `make ci`.
type evalCase struct {
	fixture         string // file under testdata/detections/
	wantDisposition string // "propose" | "insufficient"
	wantContractRef string // checked only when wantDisposition == "propose"
	reasonContains  string // checked only when wantDisposition == "insufficient"; "" = any non-empty reason
}

func evalTable() []evalCase {
	return []evalCase{
		{
			fixture:         "node-death.yaml",
			wantDisposition: "propose",
			wantContractRef: "hold-rebalance",
		},
		{
			fixture:         "argocd-sync-burn.yaml",
			wantDisposition: "insufficient",
		},
		// Live incident, rook-gce-k3s 2026-07-13 (thump-running-notes.md
		// "2026-07-13 (part 2)"): PG-starved the RGW data pool, rattle fired
		// slo_burn:ceph-osd, and the reasoner declared resource_exhaustion/
		// unknown and proposed hold-rebalance instead of declining. No
		// catalog action maps to ceph-osd-latency's failure class, so the
		// correct disposition is insufficient. Expect this RED today — same
		// discrimination bug as the RGW/dependency_saturation case this
		// branch exists to fix.
		{
			fixture:         "ceph-osd-latency.yaml",
			wantDisposition: "insufficient",
		},
	}
}

// evalEvidence is the canned Prometheus state each fixture's MetricsTool
// should see — the cluster as it actually was at the moment the incident
// was captured, not whatever the live rig happens to read today. A fixture
// pins one historical moment; querying a real, currently-running
// Prometheus would answer a different question on every run (and require
// the rig to be up at all), defeating the point of a committed corpus.
func evalEvidence(fixture string) map[string]string {
	switch fixture {
	case "node-death.yaml":
		// A worker node dropped while the cluster was already tight on
		// capacity: one OSD down, PGs degraded, recovery under way, and
		// fullest_pool_ratio past Ceph's own nearfull threshold (0.85) —
		// losing more capacity to a rebalance right now is a real risk, not
		// a hypothetical one. Deliberately unambiguous: the first version of
		// this fixture read as "plenty of headroom" (0.31/0.42) and the
		// model flip-flopped on whether to decline once seedPrompt started
		// warning it off over-crediting recovery activity as
		// resource_exhaustion — these numbers remove that ambiguity instead
		// of relying on the model to infer urgency from OSD count alone.
		return map[string]string{
			"ceph_health":           "1", // WARN
			"osds_down":             "1",
			"osds_out":              "0",
			"pgs_degraded":          "48",
			"pgs_backfilling":       "0",
			"recovery_active":       "120",
			"mons_in_quorum":        "3",
			"cluster_used_ratio":    "0.79",
			"fullest_pool_ratio":    "0.91",
			"osd_write_latency_ms":  "12",
			"rgw_request_rate":      "40",
			"rgw_failed_rate":       "0",
			"nodes_not_ready":       "1",
			"rook_pods_not_running": "1",
		}
	case "ceph-osd-latency.yaml":
		// The 2026-07-13 PG-starvation incident (thump-running-notes.md
		// "2026-07-13 (part 2)"): plenty of free capacity (not
		// resource_exhaustion), RGW writes succeeding just slow (not
		// dependency_saturation either) — the PG merge itself is the
		// cause, and nothing in the catalog names that. Correct call is
		// insufficient.
		return map[string]string{
			"ceph_health":           "0",
			"osds_down":             "0",
			"osds_out":              "0",
			"pgs_degraded":          "0",
			"pgs_backfilling":       "40", // the PG merge in flight, not a fault
			"recovery_active":       "18",
			"mons_in_quorum":        "3",
			"cluster_used_ratio":    "0.18",
			"fullest_pool_ratio":    "0.24",
			"osd_write_latency_ms":  "260", // the actual SLO-burning symptom
			"rgw_request_rate":      "126", // s3-traffic-generator load, per the notes
			"rgw_failed_rate":       "0",
			"nodes_not_ready":       "0",
			"rook_pods_not_running": "0",
		}
	case "argocd-sync-burn.yaml":
		// Ceph itself is healthy throughout; only ArgoCD's sync state is
		// off. No catalog action addresses that at all.
		return map[string]string{
			"ceph_health":             "0",
			"osds_down":               "0",
			"osds_out":                "0",
			"pgs_degraded":            "0",
			"pgs_backfilling":         "0",
			"recovery_active":         "0",
			"mons_in_quorum":          "3",
			"cluster_used_ratio":      "0.2",
			"fullest_pool_ratio":      "0.3",
			"osd_write_latency_ms":    "8",
			"rgw_request_rate":        "12",
			"rgw_failed_rate":         "0",
			"nodes_not_ready":         "0",
			"rook_pods_not_running":   "0",
			"argocd_apps_out_of_sync": "1",
		}
	default:
		return nil
	}
}

// newFakePrometheus stands in for the rig's real Prometheus: same
// MetricsTool, same query dispatch, only the HTTP backend is canned — the
// production code path (metrics_tool.go) is exercised unchanged.
func newFakePrometheus(t *testing.T, queries map[string]string, values map[string]string) *httptest.Server {
	t.Helper()
	byPromQL := make(map[string]string, len(values))
	for name, val := range values {
		promQL, ok := queries[name]
		if !ok {
			t.Fatalf("evalEvidence names unknown evidence query %q — check evidence-queries.yaml", name)
		}
		byPromQL[promQL] = val
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		val, ok := byPromQL[r.URL.Query().Get("query")]
		if !ok {
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,%q]}]}}`, val)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestEval_ReasonerAgainstProductionCatalog scores the table above against a
// real Anthropic model. It is gated on ANTHROPIC_API_KEY — no key, no
// asserts, just a skip — so an accidental `go test ./...` (without -tags
// eval this file isn't even compiled in, but belt and suspenders) never
// spends a token or fails a build that can't reach the network.
func TestEval_ReasonerAgainstProductionCatalog(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY unset — the eval harness needs a real model; see `make eval`")
	}

	transcripts := os.Getenv("CLANK_EVAL_TRANSCRIPTS")
	if transcripts == "" {
		transcripts = filepath.Join(os.TempDir(), "clank-eval-transcripts")
	}
	if err := os.MkdirAll(transcripts, 0o750); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	t.Logf("transcripts (read these when a row fails): %s", transcripts)

	queries, err := LoadEvidenceQueries(filepath.Join("..", "..", "config", "rook-gce-k3s", "whir", "evidence-queries.yaml"))
	if err != nil {
		t.Fatalf("load evidence queries: %v", err)
	}

	for _, tc := range evalTable() {
		t.Run(tc.fixture, func(t *testing.T) {
			det := loadDetectionFixture(t, tc.fixture)

			prom := newFakePrometheus(t, queries, evalEvidence(tc.fixture))
			tools := map[string]Tool{"metrics": &MetricsTool{BaseURL: prom.URL, Queries: queries}}

			l := newLoop("", t.TempDir(), t.TempDir(),
				NewAnthropicModel(apiKey), tools,
				NewIntake(noopTopology{}, noopChange{}),
				contract.Default(),
				contract.DefaultFailureClasses(),
				NewDirStore(transcripts),
				noop.Tracer{}, nil)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			set, err := l.Engine.Propose(ctx, det)
			if err != nil {
				t.Fatalf("Propose: %v (see %s/%s.jsonl)", err, transcripts, det.Fingerprint)
			}

			switch tc.wantDisposition {
			case "propose":
				if len(set.Proposals) == 0 {
					t.Fatalf("want a proposal, got none — status: %+v (see %s/%s.jsonl)",
						set.Status, transcripts, det.Fingerprint)
				}
				if got := set.Proposals[0].ContractRef; got != tc.wantContractRef {
					t.Errorf("ContractRef = %q, want %q (see %s/%s.jsonl)",
						got, tc.wantContractRef, transcripts, det.Fingerprint)
				}
			case "insufficient":
				if len(set.Proposals) != 0 {
					t.Fatalf("want insufficient, got %d proposal(s) (see %s/%s.jsonl)",
						len(set.Proposals), transcripts, det.Fingerprint)
				}
				if set.Status == nil || set.Status.Reason == "" {
					t.Errorf("decline has no reason — Stage 1's payoff regressed")
				}
				if tc.reasonContains != "" && !strings.Contains(set.Status.Reason, tc.reasonContains) {
					t.Errorf("reason %q does not contain %q", set.Status.Reason, tc.reasonContains)
				}
			}
		})
	}
}

func loadDetectionFixture(t *testing.T, name string) signal.Detection {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "detections", name)) //nolint:gosec
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var det signal.Detection
	if err := yaml.Unmarshal(raw, &det); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return det
}
