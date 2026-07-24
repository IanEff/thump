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
		// The OTel demo domain's two remedies (Wave 6, thump-test rig
		// CLAUDE.md §8) — one per armed flagd flag. Both are BlastLow
		// (a single ConfigMap flip, no node/cluster-wide effect) and
		// reversible (flip the flag back on), so both auto-band under
		// hiss's tier-1 act_reversible ceiling. Deliberately scoped to
		// proposal.ClassServiceFailure only — see that constant's comment
		// for why the demo domain must never share a failure class with
		// Ceph's.
		{
			Name:                     "disable-product-catalog-failure",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassServiceFailure},
			ApplicableTiers:          []string{"tier-1"},
			Action: ActionSpec{
				Description: "Flip the productCatalogFailure flagd flag to \"off\" (merge-patch the " +
					"flagd-config ConfigMap in otel-demo), clearing the injected GetProduct fault; reversible.",
			},
			BlastTier: proposal.BlastLow,
			Reversal: Reversal{
				Method:   "enable-product-catalog-failure",
				Fallback: "page-oncall",
			},
			SuccessCriteria: SuccessCriteria{
				Metric: "product_catalog_error_ratio",
				Target: "product_catalog_error_ratio == 0",
				Window: 5 * time.Minute,
				// VERIFIED LIVE 2026-07-19 (Wave 5): flag off -> ratio back
				// to 0 within ~40-60s of the ConfigMap patch propagating.
				// 0.9 not 1.0 leaves headroom for scrape-interval noise
				// rather than demanding an exact zero.
				SeverityQuery:        "severity_product_catalog_availability",
				SeverityReductionPct: 0.9,
			},
		},
		{
			// cart is the ranker's two-eligible-action case (Wave 7):
			// cartFailure is also "fixable" by restarting the cart pod, but
			// the fault is flag state, not pod state, so only this action
			// actually clears it. Authored the same as
			// disable-product-catalog-failure otherwise.
			Name:                     "disable-cart-failure",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassServiceFailure},
			ApplicableTiers:          []string{"tier-1"},
			Action: ActionSpec{
				Description: "Flip the cartFailure flagd flag to \"off\" (merge-patch the flagd-config " +
					"ConfigMap in otel-demo), clearing the injected EmptyCart RPC fault (checkout -> " +
					"CartService gRPC status 9); reversible.",
			},
			BlastTier: proposal.BlastLow,
			Reversal: Reversal{
				Method:   "enable-cart-failure",
				Fallback: "page-oncall",
			},
			SuccessCriteria: SuccessCriteria{
				Metric:               "cart_error_ratio",
				Target:               "cart_error_ratio == 0",
				Window:               5 * time.Minute,
				SeverityQuery:        "severity_cart_availability",
				SeverityReductionPct: 0.9,
			},
		},
		{
			// cart's plausible-but-wrong alternative (Wave 7's ranker
			// exercise, see disable-cart-failure's comment above): a real,
			// authored action, not a strawman — BlastLow and reversible so
			// it clears hiss's gate on its own merits, the same as
			// disable-cart-failure. What discriminates them is the honest
			// SeverityReductionPct below: cartFailure is flagd-controlled
			// flag state, not pod state, so recycling cart's pods doesn't
			// touch the fault. Authored low rather than 0 — a restart isn't
			// provably inert (e.g. it would clear a genuinely wedged
			// process), just ineffective against this failure class.
			Name:                     "restart-cart-pod",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassServiceFailure},
			ApplicableTiers:          []string{"tier-1"},
			Action: ActionSpec{
				Description: "Roll the cart Deployment's pods (merge-patch a pod-template " +
					"restart annotation, the same mechanism `kubectl rollout restart` uses); " +
					"reversible in the sense that repeating it is harmless, but it does not " +
					"clear a flagd-controlled fault.",
			},
			BlastTier: proposal.BlastLow,
			Reversal: Reversal{
				Method:   "restart-cart-pod",
				Fallback: "page-oncall",
			},
			SuccessCriteria: SuccessCriteria{
				Metric:               "cart_error_ratio",
				Target:               "cart_error_ratio == 0",
				Window:               5 * time.Minute,
				SeverityQuery:        "severity_cart_availability",
				SeverityReductionPct: 0.1,
			},
		},
		{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Action: ActionSpec{
				Description: "Scale the non-critical s3-traffic-generator deployment down " +
					"to a floor, shedding synthetic load from the saturated RGW path; " +
					"reversible by restoring the baseline replica count.",
				ScopeParameters: map[string]Range{"throttle_replicas": {Max: 5, Default: 2}},
			},
			BlastTier: proposal.BlastMed,
			Reversal: Reversal{
				Method:   "restore-traffic-baseline",
				Fallback: "page-oncall",
			},
			SuccessCriteria: SuccessCriteria{
				Metric:               "rgw_get_put_latency_ms",
				Target:               "rgw_get_put_latency_ms < 150",
				Window:               10 * time.Minute,
				SeverityQuery:        "severity_rgw_saturation",
				SeverityReductionPct: 0.7,
			},
		},
	})
}
