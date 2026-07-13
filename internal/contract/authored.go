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
				Description:     "Throttle non-critical request paths at the ingress",
				ScopeParameters: map[string]Range{"throttle_pct": {Min: 10, Max: 60, Default: 25}},
			},
			Reversal:        Reversal{Method: "unthrottle", Fallback: "page-oncall"},
			SuccessCriteria: SuccessCriteria{Metric: "latency_p99", Target: "p99 < 250ms", Window: 10 * time.Minute},
		},
		{
			Name: "hold-rebalance",
			// unknown deliberately NOT listed: mapping "I don't know" to a
			// real action gave the model an escape hatch to act instead of
			// declining (thump-running-notes.md 2026-07-13) — a mismatch
			// between declared class and proposed action now becomes an
			// auditable decline (engine.go's errClassMismatch), not silence.
			ApplicableFailureClasses: []proposal.FailureClass{
				proposal.ClassResourceExhaustion,
			},
			ApplicableTiers: []string{"tier-1"},
			Action: ActionSpec{
				Description: "Hold Ceph recovery/rebalancing (osd set noout) while a " +
					"node is transiently out, so the cluster doesn't thrash; reversible.",
				ScopeParameters: map[string]Range{
					"hold_minutes": {Min: 5, Max: 60, Default: 15},
				},
			},
			Reversal: Reversal{
				Method:   "release-rebalance",
				Fallback: "page-oncall",
			},
			SuccessCriteria: SuccessCriteria{
				Metric: "ceph_health",
				Target: "HEALTH_OK",
				Window: 10 * time.Minute,
			},
		},
	})
}
