package contract

import (
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
)

// Default is the compiled-in authored action catalog: the single source of
// the actions the engine may propose (clank) and execute (thump). Both beats
// call this so they always reason and act over the identical set.
//
// The catalog is Go, not config, because Precondition.OK is a func(SAO) bool
// and cannot ride YAML — the day a precondition DSL exists, this can move to a
// loaded file.
func Default() *StaticCatalog {
	return NewStaticCatalog([]ActionContract{
		{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Action: ActionSpec{
				Description: "Rate-limit anonymous (unauthenticated) RGW requests via radosgw-admin's " +
					"global ratelimit, shedding non-critical load without touching authenticated request paths",
				ScopeParameters: map[string]Range{"throttle_pct": {Min: 10, Max: 60, Default: 25}},
			},
			BlastTier: proposal.BlastMed,
			Reversal:  Reversal{Method: "unthrottle", Fallback: "page-oncall"},
			SuccessCriteria: SuccessCriteria{
				Metric: "latency_p99", Target: "p99 < 250ms", Window: 10 * time.Minute,
				SeverityQuery: "severity_rgw_availability", SeverityReductionPct: 0.5,
			},
		},
		{
			// The second dependency_saturation remedy: adds capacity
			// instead of shedding load, so a proposal for this class is a
			// real trade-off between two candidates for the ranker to
			// weigh, not a rubber stamp on the only option (Phase E's E2).
			Name:                     "scale-out-rgw-gateways",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Action: ActionSpec{
				Description: "Scale up RGW gateway replicas (CephObjectStore spec.gateway.instances) " +
					"to add serving capacity under load",
				ScopeParameters: map[string]Range{"additional_replicas": {Min: 1, Max: 3, Default: 1}},
			},
			BlastTier: proposal.BlastLow,
			Reversal:  Reversal{Method: "scale-in-rgw-gateways", Fallback: "page-oncall"},
			SuccessCriteria: SuccessCriteria{
				Metric: "rgw_get_put_latency_ms", Target: "avg < 50ms", Window: 10 * time.Minute,
				SeverityQuery: "severity_rgw_saturation", SeverityReductionPct: 0.6,
			},
		},
		{
			Name: "hold-rebalance",
			// unknown deliberately NOT listed: mapping "I don't know" to a
			// real action gave the model an escape hatch to act instead of
			// declining (thump-running-notes.md 2026-07-13) — a mismatch
			// between declared class and proposed action now becomes an
			// auditable decline (engine.go's errClassMismatch), not silence.
			ApplicableFailureClasses: []proposal.FailureClass{
				proposal.ClassRedundancyDegraded,
			},
			ApplicableTiers: []string{"tier-1"},
			Action: ActionSpec{
				Description: "Hold Ceph recovery/rebalancing (osd set noout) while a " +
					"node is transiently out, so the cluster doesn't thrash; reversible.",
				ScopeParameters: map[string]Range{
					"hold_minutes": {Min: 5, Max: 60, Default: 15},
				},
			},
			BlastTier: proposal.BlastMed,
			Reversal: Reversal{
				Method:   "release-rebalance",
				Fallback: "page-oncall",
			},
			SuccessCriteria: SuccessCriteria{
				Metric:               "ceph_health",
				Target:               "HEALTH_OK",
				Window:               10 * time.Minute,
				SeverityQuery:        "severity_ceph_redundancy",
				SeverityReductionPct: 0.7,
			},
		},
		{
			// The only high-blast action in the catalog, and one of two remedies
			// for redundancy_degraded: raising recovery concurrency buys
			// durability by spending client-serving I/O — a trade a human
			// blesses, which is why it's authored BlastHigh. Reversible, so
			// the shaper holds it for a human rather than the gate refusing it.
			Name:                     "accelerate-recovery",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassRedundancyDegraded},
			ApplicableTiers:          []string{"tier-1"},
			Action: ActionSpec{
				Description: "Raise OSD recovery/backfill concurrency (osd_max_backfills, " +
					"osd_recovery_max_active) cluster-wide to restore full data replication faster, " +
					"trading client I/O headroom for durability while placement groups are degraded; reversible.",
				ScopeParameters: map[string]Range{"backfill_concurrency": {Min: 4, Max: 32, Default: 16}},
			},
			BlastTier: proposal.BlastHigh,
			Reversal: Reversal{
				Method:   "restore-recovery-defaults",
				Fallback: "page-oncall",
			},
			SuccessCriteria: SuccessCriteria{
				Metric:               "pgs_degraded",
				Target:               "pgs_degraded == 0",
				Window:               10 * time.Minute,
				SeverityQuery:        "severity_ceph_redundancy",
				SeverityReductionPct: 0.8,
			},
		},
	})
}
