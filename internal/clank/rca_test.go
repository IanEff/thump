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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/anthropic"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/reason"
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

	// wantConfidenceAtLeast grades the number ScoringWeights move, which
	// wantDisposition/wantClass/mustCite cannot: a row that reaches the right
	// class on two live backends must out-score one that reaches it on one,
	// or the grounding tiers are decorative. Zero means ungraded — every
	// "insufficient" row, since a declined set carries no candidate and so no
	// confidence at all.
	wantConfidenceAtLeast float64

	// wantCeilingBound pins whether the model's self-report was the binding
	// constraint. A row where the ceiling always binds is a row where no
	// weight change can ever reach the emitted number — a property of the
	// fixture, not of the tuning, and it belongs in the report rather than
	// being discovered halfway through a sweep. Only checked when
	// wantConfidenceAtLeast is set.
	wantCeilingBound bool
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
		// Mined live, 2026-08-02: a real chaos-mesh OSD pod-failure
		// (chaos/osd-pod-failure-accelerate.yaml) burned this signal for
		// real. In *production*, clank proposed hold-rebalance at
		// self-reported confidence 0.850 (computedConfidence 0.8663 — the
		// causal bonus from real ArgoCD change-source data pushed it above
		// the self-report there). This RCA harness cannot reproduce that
		// number: NewIntake(noopTopology{}, noopChange{}) means
		// LikelihoodOK is always false here (no causal bonus reachable at
		// all), so the achievable ceiling is whichever grounding tier this
		// fixture's citations land on — GroundingOne (0.7) if only one
		// coherent backend, GroundingMany (1.0) if two. wantConfidenceAtLeast
		// is set conservatively below GroundingOne's floor and
		// wantCeilingBound assumes the self-report (typically high on a real
		// incident) won't be the binding constraint against a 0.7 ceiling —
		// both to be corrected from what `task rca` actually measures, same
		// discipline rcaFloor's own comment already uses. Full trail in
		// hold-rebalance-osd-down.yaml's own header and
		// thump-running-notes.md (2026-08-02).
		{
			name:                  "a real OSD pod-failure holds the rebalance rather than escalating past it",
			fixture:               "hold-rebalance-osd-down.yaml",
			wantDisposition:       "propose",
			wantContractRef:       "hold-rebalance",
			wantClass:             proposal.ClassRedundancyDegraded,
			mustCite:              []string{"pgs_degraded"},
			wantConfidenceAtLeast: 0.65,
			wantCeilingBound:      false,
			evidence: map[string]string{
				"ceph_health":           "1",
				"osds_down":             "1",
				"osds_out":              "0",
				"pgs_degraded":          "70",
				"pgs_backfilling":       "0",
				"recovery_ops_rate":     "0",
				"mons_in_quorum":        "3",
				"cluster_used_ratio":    "0.0134",
				"fullest_pool_ratio":    "0.0051",
				"osd_write_latency_ms":  "15.8",
				"rgw_request_rate":      "0.2",
				"rgw_failed_rate":       "0",
				"nodes_not_ready":       "0",
				"rook_pods_not_running": "4",
			},
		},
		// Mined live, same run and same root cause as the row above — one
		// fault, two distinct rattle signals (ceph-health vs ceph-redundancy).
		// In *production*, clank proposed accelerate-recovery at
		// self-reported confidence 0.866 (computedConfidence 0.8660, a real
		// causal bonus, not ceiling bound). This RCA harness has no causal
		// engine reachable (see the row above's comment) — wantConfidenceAtLeast
		// and wantCeilingBound are provisional, set below GroundingOne's 0.7
		// floor, and meant to be corrected from what `task rca` actually
		// measures. This is the catalog's one high-blast action
		// (catalog.yaml's own comment: "a trade a human blesses"), so hiss
		// correctly held on risk_ceiling in production — approved by hand,
		// executed live, mutation verified directly on the cluster. Full
		// trail in accelerate-recovery-cephblockpool.yaml's own header and
		// thump-running-notes.md (2026-08-02).
		{
			name:                  "a real OSD pod-failure proposes the high-blast recovery accelerant, held for a human",
			fixture:               "accelerate-recovery-cephblockpool.yaml",
			wantDisposition:       "propose",
			wantContractRef:       "accelerate-recovery",
			wantClass:             proposal.ClassRedundancyDegraded,
			mustCite:              []string{"pgs_degraded"},
			wantConfidenceAtLeast: 0.65,
			wantCeilingBound:      false,
			evidence: map[string]string{
				"ceph_health":           "1",
				"osds_down":             "1",
				"osds_out":              "0",
				"pgs_degraded":          "70",
				"pgs_backfilling":       "0",
				"recovery_ops_rate":     "0",
				"mons_in_quorum":        "3",
				"cluster_used_ratio":    "0.0135",
				"fullest_pool_ratio":    "0.0051",
				"osd_write_latency_ms":  "13.6",
				"rgw_request_rate":      "0.2",
				"rgw_failed_rate":       "0",
				"nodes_not_ready":       "0",
				"rook_pods_not_running": "4",
			},
		},
		// Mined live, 2026-08-02: the OTel-demo domain's first propose row in
		// this table — cartFailure flagd flag flipped on for real, confirmed
		// by cart's own FailedPrecondition/redis errors against real
		// load-generator traffic. In *production*, clank proposed
		// disable-cart-failure at self-reported confidence 0.8406
		// (computedConfidence 0.8406, not ceiling bound). This RCA harness's
		// kube fake only carries rook-ceph pod data (see the tools map
		// above), nothing for cart, so this row can only ever corroborate
		// through "metrics" — wantConfidenceAtLeast is set below
		// GroundingOne's 0.7 floor and corrected from what `task rca`
		// actually measures, same as the two Ceph rows above. mustCite pins
		// cart_error_ratio only — it's the action's own successCriteria
		// metric (catalog.yaml) and certain to have been cited; the full
		// Citations list from the sealed Set is **not recoverable** — clank
		// was scaled to 0 to stop residual burn-rate re-detection cycles
		// before this Set's WAL segment sealed (WAL_DIR is an ephemeral
		// emptyDir; an unsealed segment dies with the pod), a real mistake
		// made this same session (thump-running-notes.md 2026-08-02). This
		// row asserts only the one citation that's certain regardless.
		// wantContractRef pins
		// disable-cart-failure specifically, not restart-cart-pod — the
		// catalog's other eligible action for this class and the exact
		// discrimination this fixture exists to test (catalog.yaml:169-175's
		// own comment on what tells the two apart).
		{
			name:                  "a real cartFailure flag flip proposes the flag fix, not the pod restart",
			fixture:               "disable-cart-failure.yaml",
			wantDisposition:       "propose",
			wantContractRef:       "disable-cart-failure",
			wantClass:             proposal.ClassServiceFailure,
			mustCite:              []string{"cart_error_ratio"},
			wantConfidenceAtLeast: 0.65,
			wantCeilingBound:      false,
			evidence: map[string]string{
				"slo_burn_cart":              "50",
				"severity_cart_availability": "0.5",
				"cart_error_ratio":           "0.4737",
				"demo_pods_not_running":      "0",
			},
		},
		// ceph-rgw-saturation.yaml's own header names the full trail: real
		// traffic-loaded RGW CPU stress, rgw_get_put_latency_ms held
		// 173-179ms against a 150ms SLO, 0 failed requests, healthy upstream
		// OSD/capacity. The live run correctly ruled out resource_exhaustion
		// but landed on traffic_shift (no catalog action, zero proposals) —
		// a third documented decision boundary alongside node-death.yaml and
		// ceph-osd-latency.yaml, not a regression. Evidence copied verbatim
		// from eval_test.go's evalEvidence (same fixture, same provenance:
		// RunID slo_burn:ceph-rgw/1784045518809851063, step 3) rather than
		// re-derived — coverage only, this row adds no confidence data since
		// insufficient carries no candidate.
		//
		// knownMiss, measured rather than assumed: 2 runs while widening this
		// table (2026-08-02) scored MISS (proposed dependency_saturation) then
		// PASS (correctly declined) — the same flip-flop rgw-degradation.yaml's
		// own knownMiss already documents, on the same fixture eval_test.go
		// independently calls a decision boundary. See rcaFloor.
		// UPDATE: ceph-rgw-saturation.yaml: real traffic-loaded RGW CPU stress,
		// rgw_get_put_latency_ms held 173-179ms against a 150ms SLO, 0 failed
		// requests, healthy upstream OSD/capacity. Declining is correct
		// catalog-bounded behaviour — dependency_saturation holds no active
		// catalog remedy, so the reasoner must decline rather than invent an
		// uncatalogued action
		{
			name:            "an RGW CPU-saturation burn declines rather than mislabeling traffic_shift as dependency_saturation",
			fixture:         "ceph-rgw-saturation.yaml",
			wantDisposition: "insufficient",
			evidence: map[string]string{
				"ceph_health":             "0",
				"osds_down":               "0",
				"osds_out":                "0",
				"pgs_degraded":            "0",
				"pgs_backfilling":         "0",
				"recovery_ops_rate":       "0",
				"mons_in_quorum":          "3",
				"cluster_used_ratio":      "0.0997",
				"fullest_pool_ratio":      "0.02",
				"osd_write_latency_ms":    "16.9",
				"rgw_request_rate":        "209.4",
				"rgw_failed_rate":         "0",
				"rgw_get_put_latency_ms":  "173.2",
				"slo_burn_rgw":            "0",
				"slo_burn_rgw_saturation": "60",
				"nodes_not_ready":         "0",
				"rook_pods_not_running":   "0",
			},
		},
		// ceph-cluster-burn-accel.yaml deliberately left out of this table:
		// same Name/Fingerprint/Topology as node-death.yaml (an earlier,
		// milder-Divergence capture of the same incident — Observed=14.28 vs
		// node-death's 71.4), but no transcript pull has recovered its own
		// step evidence, and inventing values on the resemblance to
		// node-death alone would be exactly the "fabricated corpus" this
		// suite's own discipline refuses. Add it once a real transcript
		// backs it, not before.
	}
}

// rcaFloor tolerates every documented known-miss row landing wrong in the
// same run — a suite that required every row green would either get a
// known-miss quietly dropped, or turn into a CI gate on model output. It is
// (total rows) − (known-miss rows), not a fraction — the table holds 8 rows
// with 2 remaining known misses (argocd-sync-burn.yaml and
// rgw-degradation.yaml). ceph-rgw-saturation.yaml is no longer a known miss
// because declining a remedy-less class is correct catalog-bounded
// behaviour. The floor is 8 − 2 = 6.
const rcaFloor = 6

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

	ev, err := evidence.LoadEvidenceConfig(filepath.Join("..", "..", "config", "thump-test", "whir", "evidence-queries.yaml"))
	if err != nil {
		t.Fatalf("load evidence queries: %v", err)
	}
	queries := ev.Queries

	table := rcaTable()
	scored := 0
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			det := loadDetectionFixture(t, tc.fixture)

			prom := newFakePrometheus(t, promQLByName(queries), tc.evidence)
			tools := map[string]reason.Tool{
				// Each EvidenceQuery carries its own Subject now, so a
				// citation against any of these queries stamps one and
				// coherentSubject (gate.go:93) can ground on it — a metrics
				// tool built without per-query subjects fails every citation
				// closed and every propose row computes at GroundingNone
				// regardless of what it cited.
				"metrics": &evidence.MetricsTool{BaseURL: prom.URL, Queries: queries},
				// A second live backend, so a citation crossing Corroborated
				// >= 2 is actually reachable — with "metrics" alone,
				// coherentLiveCitations can never count past one distinct
				// reason.Tool value and GroundingMany is structurally dead. The
				// model decides on its own whether to call this; it isn't
				// scripted, so whether any row actually reaches
				// GroundingMany is an empirical question this suite answers
				// by running, not by construction. Subjects: ev.Index is the
				// same rule set production wiring uses (clank.go:278), not a
				// hand-rolled table — real subject names differ from what
				// the golden-path tests use (ceph-osd/ceph-rgw/rook-operator/
				// cart, not a single "ceph-cluster").
				"kube": &evidence.KubeTool{
					Client: kubefake.NewSimpleClientset(&corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "rook-ceph-mon-a",
							Namespace: "rook-ceph",
							Labels:    map[string]string{"app": "rook-ceph-mon"},
						},
						Status: corev1.PodStatus{Phase: corev1.PodRunning},
					}),
					Subjects: ev.Index,
				},
			}

			l := newLoop("", t.TempDir(), t.TempDir(), t.TempDir(),
				anthropic.NewModel(apiKey, modelRequestTimeout), tools,
				NewIntake(noopTopology{}, noopChange{}),
				shippedCatalog(),
				shippedClasses(),
				NewDirStore(transcripts),
				time.Hour, noop.Tracer{}, nil, nil, DefaultScoringWeights(), DefaultLimits())

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
					switch {
					case tc.wantConfidenceAtLeast > 0 && top.Confidence < tc.wantConfidenceAtLeast:
						miss = fmt.Sprintf("Confidence = %.2f, want >= %.2f", top.Confidence, tc.wantConfidenceAtLeast)
					case tc.wantConfidenceAtLeast > 0 && top.ConfidenceCeilingBound != tc.wantCeilingBound:
						miss = fmt.Sprintf("ConfidenceCeilingBound = %v, want %v", top.ConfidenceCeilingBound, tc.wantCeilingBound)
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

			status := "PASS"
			if miss != "" {
				status = "MISS"
			}
			if len(set.Proposals) > 0 {
				top := set.Proposals[0]
				ceiling := "-"
				if top.ConfidenceCeilingBound {
					ceiling = "BOUND"
				}
				t.Logf("%-4s %-58s class=%-20s computed=%.2f emitted=%.2f ceiling=%s",
					status, tc.name, set.FailureClass, top.ComputedConfidence, top.Confidence, ceiling)
			} else {
				t.Logf("%-4s %-58s (insufficient, ungraded)", status, tc.name)
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
