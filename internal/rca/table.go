// Package rca is the graded root-cause instrument: eight scenarios whose
// evidence carries both a real signal and a planted decoy, scored on whether
// the reasoner reached the right failure class by citing the right evidence.
// It grades citation discipline, not just the verdict — a run that lands the
// right class off the decoy got there by luck.
package rca

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/whir"
)

// Case is one graded root-cause scenario: a fixture whose evidence carries
// both the real signal for WantDisposition/WantClass and a planted decoy —
// evidence that reads plausible for a different failure class and is
// genuinely present in the underlying incident, not invented to be
// unrealistic. MustCite and MustNotCite grade citation discipline, not just
// the final verdict: a run that reaches the right disposition by citing the
// decoy got there by luck, and MustNotCite catches it even when WantClass
// still matches.
type Case struct {
	Name string // the graded claim, read as a sentence in the report
	// Rig is the config/<rig> directory this row's catalog, failure classes,
	// and kube objects must be loaded from — a row graded against the wrong
	// rig's catalog resolves a contract that catalog never declares.
	Rig     string
	Fixture string // filename under internal/clank/testdata/detections/
	// Evidence is real signal + planted decoy for this row, keyed by either
	// an evidence-queries.yaml query name (fakePrometheus's lookup) or a
	// subjects: subject name (fakeLoki's) — the two key spaces are disjoint
	// on every shipped rig config, but nothing enforces that beyond
	// TestTable_GradesOnlyQueriesTheShippedEvidenceConfigDefines; a rig
	// naming a subject the same as a query would misdirect evidence
	// silently.
	Evidence map[string]string

	WantDisposition string // "propose" | "insufficient"
	WantContractRef string // checked only when WantDisposition == "propose"
	WantClass       proposal.FailureClass

	MustCite    []string // query names the top proposal must cite when WantDisposition == "propose"
	MustNotCite []string // the decoy's query name(s); documentation only on an "insufficient" row, since Status.Reason is prose rather than a structured citation list

	// KnownMiss marks a real live misfire reproduced here on purpose as a
	// documented baseline. It does NOT mean "flaky, skip" — a reader who
	// thinks it does will delete two of the eight rows. Report.Floor is
	// derived from the count of these.
	KnownMiss bool

	// WantConfidenceAtLeast grades the number ScoringWeights move, which the
	// disposition and class checks cannot: a row that reaches the right class
	// on two live backends must out-score one that reaches it on one, or the
	// grounding tiers are decorative. Zero means ungraded — every
	// "insufficient" row, since a declined set carries no candidate.
	//
	// Setting WantConfidenceAtLeast > 0 also arms the WantCeilingBound check
	// (report.go:144) — a row with WantConfidenceAtLeast set will fail if its
	// top candidate's ConfidenceCeilingBound does not match WantCeilingBound.
	WantConfidenceAtLeast float64

	// WantCeilingBound pins whether the model's self-report was the binding
	// constraint. A row where the ceiling always binds is one no weight change
	// can reach — a property of the fixture, not of the tuning, and it belongs
	// in the report rather than being discovered halfway through a sweep.
	// Checked only when WantConfidenceAtLeast > 0.
	WantCeilingBound bool

	// FaultObjects seeds the harness's kube fake with the objects this row's
	// fault actually patches — resolved through the shipped subject rules in
	// config/<rig>/whir/evidence-queries.yaml rather than fabricated.
	FaultObjects []runtime.Object

	// States maps dependency names to health states (e.g. whir.StateDegraded)
	// when a case overrides them — unlisted dependencies default to whir.StateHealthy.
	States map[string]string
}

// Table is the graded suite. Every row's comment is its provenance — why this
// fixture, which decoy was planted, and what a live run actually did — and it
// is the reason the row can be trusted as a baseline rather than re-litigated
// each time it misses.
func Table() []Case {
	return []Case{
		// Mined from a live run: no ArgoCD-specific evidence corroborated
		// the sync-burn signal, but acme-api was genuinely erroring at the
		// same moment, and the model proposed acme-shed-load off that
		// correlation instead of declining. No catalog action addresses a
		// bare sync-burn SLO alone, so the correct call is insufficient —
		// a known miss because a suite that stopped reproducing it would be
		// measuring the fixture, not the reasoner.
		{
			Name:            "argocd sync burn does not get explained away by a coincidentally-broken acme-api",
			Rig:             "thump-test",
			Fixture:         "argocd-sync-burn.yaml",
			WantDisposition: "insufficient",
			MustNotCite:     []string{"acme_api_error_ratio", "severity_acme_api_availability"},
			KnownMiss:       true,
			Evidence: map[string]string{
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
		// WantContractRef is deliberately unset — two catalogued actions
		// are legitimately applicable here and the row grades the class,
		// not the choice between them.
		{
			Name:            "a node death reads as redundancy_degraded, not a coincidental RGW request-rate reading",
			Rig:             "thump-test",
			Fixture:         "node-death.yaml",
			WantDisposition: "propose",
			WantClass:       proposal.ClassRedundancyDegraded,
			MustCite:        []string{"pgs_degraded"},
			MustNotCite:     []string{"rgw_request_rate", "rgw_failed_rate"},
			Evidence: map[string]string{
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

		// rgw_request_rate=126 is real, elevated load that reads like
		// dependency_saturation at a glance — it isn't, rgw_failed_rate
		// stays 0 throughout, and the actual cause (a PG merge) has no
		// catalog action at all. Correct call is insufficient; citing the
		// load figure as if it explained anything is the miss this row
		// catches.
		{
			Name:            "a PG-merge latency spike does not get explained away by ordinary RGW load",
			Rig:             "thump-test",
			Fixture:         "ceph-osd-latency.yaml",
			WantDisposition: "insufficient",
			MustNotCite:     []string{"rgw_request_rate"},
			Evidence: map[string]string{
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

		// recovery_ops_rate=11366 (ops/sec, not a PG count) plus a
		// genuinely nonzero rook_pods_not_running=4 read as RGW capacity
		// exhaustion once, but RGW's own failure signal stayed negligible
		// (0.34% at 0.1 req/s) throughout — correct call is insufficient.
		// A known miss because this decoy is a genuine decision boundary
		// the model lands on either side of between runs.
		{
			Name:            "an RGW-availability burn does not get explained away by an unrelated recovery-ops reading",
			Rig:             "thump-test",
			Fixture:         "rgw-degradation.yaml",
			WantDisposition: "insufficient",
			MustNotCite:     []string{"recovery_ops_rate", "rook_pods_not_running"},
			KnownMiss:       true,
			Evidence: map[string]string{
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

		// A real OSD pod-failure signal, now carrying the topology and
		// change data the harness's Intake actually reads: ceph-osd
		// resolves in-topology and degraded, and the OSD pod restart that
		// preceded the failure is a change event targeting it — the causal
		// scorer's LiveCorroborated case. Kept single-backend on purpose
		// (metrics only, same as before): a second live backend here would
		// push this row's grounding tier to groundingMany, which is fixed at
		// 1.0 rather than swept — internal/tune's grid varies groundingNone
		// and groundingOne only, and this is the one row in the pinned
		// corpus whose ceiling-bound flips across that range, so pinning it
		// to groundingMany would erase the grid's only variance. Causal
		// firing alone already moves WantCeilingBound from false to true —
		// this row now measures the causal term, not the corroboration one.
		{
			Name:                  "a real OSD pod-failure holds the rebalance rather than escalating past it",
			Rig:                   "thump-test",
			Fixture:               "hold-rebalance-osd-down.yaml",
			WantDisposition:       "propose",
			WantContractRef:       "hold-rebalance",
			WantClass:             proposal.ClassRedundancyDegraded,
			MustCite:              []string{"pgs_degraded"},
			WantConfidenceAtLeast: 0.65,
			WantCeilingBound:      true,
			States: map[string]string{
				"ceph-osd": whir.StateDegraded,
			},
			FaultObjects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  "rook-ceph",
						Name:       "rook-ceph-operator",
						Generation: 1,
					},
					Status: appsv1.DeploymentStatus{
						Conditions: []appsv1.DeploymentCondition{
							{
								Type:           appsv1.DeploymentProgressing,
								Status:         corev1.ConditionTrue,
								LastUpdateTime: metav1.Time{Time: time.Date(2026, 8, 2, 11, 29, 7, 0, time.UTC)},
							},
						},
					},
				},
			},
			Evidence: map[string]string{
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

		// Same root cause as the row above — one fault, two distinct rattle
		// signals — but this row carries no Topology/Change of its own, so
		// it stays causal-blind like every other row that doesn't opt in.
		// This is the catalog's one high-blast action, so production
		// correctly holds it for a human via hiss's risk_ceiling rather than
		// auto-executing. A known miss, same as rows 1 and 4: measured
		// flip-flopping between hold-rebalance and accelerate-recovery
		// across runs on this fixture, both catalogued remedies for the
		// same class.
		{
			Name:                  "a real OSD pod-failure proposes the high-blast recovery accelerant, held for a human",
			Rig:                   "thump-test",
			Fixture:               "accelerate-recovery-cephblockpool.yaml",
			WantDisposition:       "propose",
			WantContractRef:       "accelerate-recovery",
			WantClass:             proposal.ClassRedundancyDegraded,
			MustCite:              []string{"pgs_degraded"},
			KnownMiss:             true,
			WantConfidenceAtLeast: 0.65,
			WantCeilingBound:      false,
			Evidence: map[string]string{
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

		// The cartFailure flag flip breaks cart's EmptyCart RPC. Two
		// actions are catalogued for it — restart-pod and
		// disable-cart-failure — and only the flag flip actually clears it,
		// so this row grades the choice as well as the class. The row's
		// "cart" Evidence key seeds a loki line, so this row corroborates
		// across metrics and loki (corroborated >= 2, computed 1.0,
		// ceilingBound true); WantConfidenceAtLeast ensures it clears the
		// floor with ceilingBound true.
		{
			Name:                  "a real cartFailure flag flip proposes the flag fix, not the pod restart",
			Rig:                   "dev",
			Fixture:               "disable-cart-failure.yaml",
			WantDisposition:       "propose",
			WantContractRef:       "disable-cart-failure",
			WantClass:             proposal.ClassServiceFailure,
			MustCite:              []string{"cart_error_ratio"},
			WantConfidenceAtLeast: 0.65,
			WantCeilingBound:      true,
			FaultObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "otel-demo",
						Name:            "flagd-config",
						ResourceVersion: "100",
						ManagedFields: []metav1.ManagedFieldsEntry{
							{
								Manager: "kubectl-edit",
								Time:    &metav1.Time{Time: time.Date(2026, 8, 2, 11, 40, 6, 0, time.UTC)},
							},
						},
					},
				},
			},
			Evidence: map[string]string{
				"slo_burn_cart":              "50",
				"severity_cart_availability": "0.5",
				"cart_error_ratio":           "0.4737",
				"demo_pods_not_running":      "0",
				"cart":                       "ERROR cart-6f9d4c: EmptyCart RPC failed, payment gateway timeout",
			},
		},

		// Real traffic-loaded RGW CPU stress, healthy upstream OSD/capacity,
		// no catalog remedy for dependency_saturation — the reasoner must
		// decline rather than invent an uncatalogued action. Not a known
		// miss: declining a remedy-less class is the correct call, so a run
		// that mislabels this is a real reasoner regression, not a
		// documented baseline to tolerate.
		{
			Name:            "an RGW CPU-saturation burn declines rather than mislabeling traffic_shift as dependency_saturation",
			Rig:             "thump-test",
			Fixture:         "ceph-rgw-saturation.yaml",
			WantDisposition: "insufficient",
			Evidence: map[string]string{
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

		// acme-api-failure.yaml's own header documents the source: queried
		// against the live rig's own Prometheus at the detection's frozen
		// timestamp (21:58:07.473933Z), not estimated. The connection-pool
		// leak moves both acme-api's request error ratio and acme-db's
		// connection saturation together — MustCite pins the row to the
		// dependency-saturation signal specifically, since a run citing only
		// the request error ratio would be as plausible a case for
		// service_failure, and this row grades that it lands on the class
		// the fault actually is. The row's "acme-api" Evidence key seeds a
		// loki line from a clean live run, so this row corroborates across
		// metrics and loki (corroborated >= 2, computed 1.0, ceilingBound true);
		// WantConfidenceAtLeast ensures it clears the floor with ceilingBound true.
		{
			Name:                  "a real acme-api connection-pool leak proposes shedding load, not a bare request-error reading",
			Rig:                   "dev",
			Fixture:               "acme-api-failure.yaml",
			WantDisposition:       "propose",
			WantContractRef:       "acme-shed-load",
			WantClass:             proposal.ClassDependencySaturation,
			MustCite:              []string{"acme_db_connections_saturation"},
			WantConfidenceAtLeast: 0.65,
			WantCeilingBound:      true,
			FaultObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:       "acme",
						Name:            "acme-fault-flag",
						ResourceVersion: "200",
						ManagedFields: []metav1.ManagedFieldsEntry{
							{
								Manager: "kubectl-edit",
								Time:    &metav1.Time{Time: time.Date(2026, 8, 16, 21, 57, 7, 0, time.UTC)},
							},
						},
					},
				},
			},
			Evidence: map[string]string{
				"acme_api_error_ratio":           "0.504",
				"acme_db_connections_saturation": "0.667",
				"acme-api":                       `127.0.0.1 - - [15/Aug/2026 21:54:59] "GET /fail HTTP/1.1" 500 -`,
			},
		},

		// ceph-cluster-burn-accel.yaml is deliberately excluded: it shares
		// node-death.yaml's Name/Fingerprint/Topology from an earlier,
		// milder capture of the same incident, but no transcript has
		// recovered its own step evidence — inventing values from
		// resemblance alone would be exactly the fabricated-evidence
		// problem this suite refuses to allow.
	}
}
