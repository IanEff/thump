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
