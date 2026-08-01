//go:build eval

package clank

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/proposal"
)

// rcaCase is one graded root-cause scenario: a fixture whose evidence carries
// both the real signal for wantDisposition/wantClass and a planted decoy —
// evidence that reads plausible for a different failure class and is
// genuinely present in the underlying incident, not invented to be
// unrealistic. mustCite/mustNotCite grade citation discipline, not just the
// final verdict: a run that reaches the right disposition by citing the
// decoy got there by luck, and mustNotCite catches it even when wantClass
// still matches.
//
// This is a different claim from evalTable's: eval asks "did the reasoner
// reach the right disposition against the production catalog," this asks
// "did it get there by grounding the right evidence, or by getting lucky
// next to a red herring." Deterministic on purpose — see
// TestRCA_ReachesTheCorrectFailureClassNotTheDecoy's own doc comment for why
// a second model is not the fix if this one drifts.
type rcaCase struct {
	name            string
	fixture         string            // testdata/detections/*.yaml — same fixture format eval uses
	evidence        map[string]string // real signal + planted decoy, keyed by evidence-queries.yaml query name
	wantDisposition string            // "propose" | "insufficient"
	wantContractRef string            // checked only when wantDisposition == "propose"
	wantClass       proposal.FailureClass
	mustCite        []string // query names the top proposal must cite when wantDisposition == "propose"
	mustNotCite     []string // the decoy's query name(s) — asserted against the top proposal's Citations when wantDisposition == "propose"; documentation only on an "insufficient" row, since Status.Reason is prose, not a structured citation list (see the test's own comment)
	knownMiss       bool     // a real live misfire, reproduced here as a documented baseline — see rcaFloor
}

func rcaTable() []rcaCase {
	return []rcaCase{
		// The flagship case, and the one row this suite exists to keep
		// honest: mined verbatim from the live transcript at
		// evidence/2026-07-26-argocd-acme-misattribution/ (vault), RunID
		// slo_burn:argocd/1785097470705542441. The signal is argocd's own
		// sync-burn SLO; the model had no ArgoCD-specific corroborating
		// evidence (its one pod-list call came back RBAC-forbidden), found
		// acme-api genuinely returning 100% errors at the same moment, and
		// proposed acme-shed-load on a service_failure diagnosis that
		// explains acme's own outage, not argocd's. The correlation was
		// real; the causal claim was not — no catalog action addresses a
		// bare sync-burn SLO in isolation, so the correct call was
		// insufficient. Recorded as a known miss, not tuned to pass: the
		// live run got this wrong once already, and a suite that quietly
		// stopped reproducing it would be measuring the fixture, not the
		// reasoner.
		{
			name:            "argocd sync burn does not get explained away by a coincidentally-broken acme-api",
			fixture:         "argocd-sync-burn.yaml",
			wantDisposition: "insufficient",
			mustNotCite:     []string{"acme_api_error_ratio", "severity_acme_api_availability"},
			knownMiss:       true,
			evidence: map[string]string{
				"ceph_health":             "0",
				"osds_down":               "0",
				"osds_out":                "0",
				"pgs_degraded":            "0",
				"pgs_backfilling":         "0",
				"recovery_ops_rate":       "0",
				"mons_in_quorum":          "3",
				"cluster_used_ratio":      "0.2",
				"fullest_pool_ratio":      "0.3",
				"osd_write_latency_ms":    "8",
				"rgw_request_rate":        "12",
				"rgw_failed_rate":         "0",
				"nodes_not_ready":         "0",
				"rook_pods_not_running":   "0",
				"argocd_apps_out_of_sync": "1",
				// The decoy — real values from the transcript's own final
				// tool calls, not invented: acme-api was genuinely down.
				"acme_api_error_ratio":           "1",
				"severity_acme_api_availability": "1",
			},
		},
		// node-death.yaml's own comment already names the ambiguity this
		// case grades: fullest_pool_ratio was raised to 0.91 specifically so
		// the model doesn't need to infer urgency from OSD count alone. The
		// decoy added here is a different axis of the same trap —
		// rgw_request_rate/rgw_failed_rate describe live traffic on a
		// completely healthy path (0 failures), which a run grounding
		// redundancy_degraded correctly has no reason to cite at all.
		//
		// wantContractRef deliberately unset: catalog.yaml names
		// hold-rebalance and accelerate-recovery as "two remedies for
		// redundancy_degraded" — this row grades the diagnosis (class +
		// citations), not which of the two valid remedies the model picked.
		{
			name:            "a node death reads as redundancy_degraded, not a coincidental RGW request-rate reading",
			fixture:         "node-death.yaml",
			wantDisposition: "propose",
			wantClass:       proposal.ClassRedundancyDegraded,
			mustCite:        []string{"pgs_degraded"},
			mustNotCite:     []string{"rgw_request_rate", "rgw_failed_rate"},
			evidence: map[string]string{
				"ceph_health":           "1",
				"osds_down":             "1",
				"osds_out":              "0",
				"pgs_degraded":          "48",
				"pgs_backfilling":       "0",
				"recovery_ops_rate":     "120",
				"mons_in_quorum":        "3",
				"cluster_used_ratio":    "0.79",
				"fullest_pool_ratio":    "0.91",
				"osd_write_latency_ms":  "12",
				"rgw_request_rate":      "40",
				"rgw_failed_rate":       "0",
				"nodes_not_ready":       "1",
				"rook_pods_not_running": "1",
			},
		},
		// The 2026-07-13 PG-starvation incident again (see evalEvidence's
		// own entry for the full trail): rgw_request_rate=126 is the
		// s3-traffic-generator's real load, present and elevated, and looks
		// like dependency_saturation's own evidence shape at a glance. It
		// isn't — rgw_failed_rate stays 0 throughout, and the actual cause
		// (a PG merge) has no catalog action at all. Correct call is
		// insufficient; citing the load figure as if it explained anything
		// is the miss this row exists to catch.
		{
			name:            "a PG-merge latency spike does not get explained away by ordinary RGW load",
			fixture:         "ceph-osd-latency.yaml",
			wantDisposition: "insufficient",
			mustNotCite:     []string{"rgw_request_rate"},
			evidence: map[string]string{
				"ceph_health":           "0",
				"osds_down":             "0",
				"osds_out":              "0",
				"pgs_degraded":          "0",
				"pgs_backfilling":       "40",
				"recovery_ops_rate":     "18",
				"mons_in_quorum":        "3",
				"cluster_used_ratio":    "0.18",
				"fullest_pool_ratio":    "0.24",
				"osd_write_latency_ms":  "260",
				"rgw_request_rate":      "126",
				"rgw_failed_rate":       "0",
				"nodes_not_ready":       "0",
				"rook_pods_not_running": "0",
			},
		},
		// The historical misread rgw-degradation.yaml's own comment names
		// verbatim: recovery_ops_rate=11366 (ops/sec, not a PG count) plus a
		// genuinely nonzero rook_pods_not_running=4 read as RGW capacity
		// exhaustion in the live run that produced this fixture, driving a
		// wrong hold-rebalance. RGW's own failure signal was negligible
		// (0.34% at 0.1 req/s) the whole time. Correct call is insufficient;
		// this is the exact decoy pair the fixture exists to reproduce.
		//
		// A documented second known miss, not a first one waiting to be
		// found: eval_test.go already calls this fixture a decision
		// boundary the real model lands on either side of between runs, and
		// two rca runs while authoring this suite reproduced exactly that —
		// this decoy is the harder one, and the suite's floor accounts for
		// it rather than hiding it by loosening the assertion.
		{
			name:            "an RGW-availability burn does not get explained away by an unrelated recovery-ops reading",
			fixture:         "rgw-degradation.yaml",
			wantDisposition: "insufficient",
			mustNotCite:     []string{"recovery_ops_rate", "rook_pods_not_running"},
			knownMiss:       true,
			evidence: map[string]string{
				"ceph_health":           "0",
				"osds_down":             "0",
				"osds_out":              "0",
				"pgs_degraded":          "0",
				"pgs_backfilling":       "0",
				"recovery_ops_rate":     "11366",
				"mons_in_quorum":        "3",
				"cluster_used_ratio":    "0.0715",
				"fullest_pool_ratio":    "0.02",
				"osd_write_latency_ms":  "10.8",
				"rgw_request_rate":      "0.1074",
				"rgw_failed_rate":       "0.0034",
				"slo_burn_rgw":          "34.28",
				"nodes_not_ready":       "0",
				"rook_pods_not_running": "4",
			},
		},
	}
}

// rcaFloor tolerates both documented known-miss rows landing wrong in the
// same run — a suite that required every row green would either get a
// known-miss quietly dropped, or turn into a CI gate on model output, which
// is the thing this instrument deliberately declines to be (see the test's
// own doc comment). Measured, not guessed: two live runs while authoring
// this suite scored 1/4 and (in an earlier revision, before node-death's
// over-strict wantContractRef was relaxed) 3/4 — both known-miss rows can
// miss together without the other two regressing, which is the actual floor
// this suite is willing to tolerate. Three or four rows missing in the same
// run is the real signal, not model noise.
const rcaFloor = 2

// TestRCA_ReachesTheCorrectFailureClassNotTheDecoy is the instrument phase
// X3 calls for: not "did the action work" (click's question), but "did the
// reasoner reach the right failure class, grounded in the evidence that
// actually distinguishes it from a planted decoy." Deterministic on
// purpose — exact FailureClass match, required-citation coverage, decoy-
// citation absence, no second model. An instrument whose job is judging an
// unmeasured reasoner cannot itself be unmeasured; do not "improve" this
// into an LLM judge.
//
// Key-gated like TestEval_ReasonerAgainstProductionCatalog: no key, no
// asserts, just a skip, so this never spends a token or blocks a build that
// can't reach the network. Not part of task ci — `task rca` runs it.
func TestRCA_ReachesTheCorrectFailureClassNotTheDecoy(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY unset — the RCA harness needs a real model; see `task rca`")
	}

	transcripts := os.Getenv("CLANK_RCA_TRANSCRIPTS")
	if transcripts == "" {
		transcripts = filepath.Join(os.TempDir(), "clank-rca-transcripts")
	}
	if err := os.MkdirAll(transcripts, 0o750); err != nil {
		t.Fatalf("mkdir transcripts: %v", err)
	}
	t.Logf("transcripts (read these when a row misses): %s", transcripts)

	ev, err := LoadEvidenceConfig(filepath.Join("..", "..", "config", "thump-test", "whir", "evidence-queries.yaml"))
	if err != nil {
		t.Fatalf("load evidence queries: %v", err)
	}
	queries := ev.Queries

	table := rcaTable()
	scored := 0
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			det := loadDetectionFixture(t, tc.fixture)

			prom := newFakePrometheus(t, queries, tc.evidence)
			tools := map[string]Tool{"metrics": &MetricsTool{BaseURL: prom.URL, Queries: queries}}

			l := newLoop("", t.TempDir(), t.TempDir(), t.TempDir(),
				NewAnthropicModel(apiKey), tools,
				NewIntake(noopTopology{}, noopChange{}),
				shippedCatalog(),
				shippedClasses(),
				NewDirStore(transcripts),
				time.Hour, noop.Tracer{}, nil, nil, DefaultScoringWeights())

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			// A Propose error is a wiring bug, not a judgment call — it fails
			// the row outright rather than scoring it as a miss.
			set, err := l.Engine.Propose(ctx, det)
			if err != nil {
				t.Fatalf("Propose: %v (see %s/%s.jsonl)", err, transcripts, det.Fingerprint)
			}

			var miss string
			switch tc.wantDisposition {
			case "propose":
				if len(set.Proposals) == 0 {
					miss = fmt.Sprintf("want a proposal, got none — status: %+v", set.Status)
					break
				}
				top := set.Proposals[0]
				switch {
				case tc.wantContractRef != "" && top.ContractRef != tc.wantContractRef:
					miss = fmt.Sprintf("ContractRef = %q, want %q", top.ContractRef, tc.wantContractRef)
				case tc.wantClass != "" && set.FailureClass != tc.wantClass:
					miss = fmt.Sprintf("FailureClass = %q, want %q", set.FailureClass, tc.wantClass)
				default:
					for _, want := range tc.mustCite {
						if !slices.Contains(top.Citations, want) {
							miss = fmt.Sprintf("citations %v missing required %q — reached the right verdict without the evidence that grounds it", top.Citations, want)
							break
						}
					}
					for _, bad := range tc.mustNotCite {
						if slices.Contains(top.Citations, bad) {
							miss = fmt.Sprintf("citations %v include decoy %q — reached the right verdict by citing the wrong evidence", top.Citations, bad)
							break
						}
					}
				}
			case "insufficient":
				// mustNotCite is not graded here: Status.Reason is prose the
				// model writes freely, not the structured, exact-match
				// Citations a Candidate carries. A decline that explains
				// "rgw_request_rate is normal, so this isn't
				// dependency_saturation" mentions the decoy's name while
				// correctly ruling it out — a substring check can't tell
				// that apart from actually leaning on it, so this row only
				// grades the disposition, same as evalCase does.
				switch {
				case len(set.Proposals) != 0:
					miss = fmt.Sprintf("want insufficient, got %d proposal(s)", len(set.Proposals))
				case set.Status == nil || set.Status.Reason == "":
					miss = "decline has no reason — Stage 1's payoff regressed"
				}
			}

			if miss != "" {
				if tc.knownMiss {
					t.Logf("documented known miss (see %s/%s.jsonl): %s", transcripts, det.Fingerprint, miss)
				} else {
					t.Logf("miss (see %s/%s.jsonl): %s", transcripts, det.Fingerprint, miss)
				}
				return
			}
			scored++
		})
	}

	t.Logf("RCA score: %d/%d rows reached the right verdict grounded in the right evidence", scored, len(table))
	if scored < rcaFloor {
		t.Errorf("scored %d/%d, below the floor of %d — more than the documented known-miss regressed", scored, len(table), rcaFloor)
	}
}
