package signal_test

import (
	"fmt"

	"github.com/ianeff/thump/api/v1/signal"
)

// Example shows a detection describing state and stopping there: an observed
// number, the baseline it drifted from, and how much of the error budget
// that burned. Nothing here says the system is unhealthy — that word is a
// conclusion, and concluding is the reasoner's job.
func Example() {
	d := signal.Detection{
		Name:          "cart-availability-burn-001",
		Fingerprint:   "fp-dummy-cart-availability-001",
		OriginService: "cart",
		ServiceTier:   "tier-1",
		DetectorType:  "burn_rate_acceleration",
		Divergence: signal.Divergence{
			Metric:     "cart_error_ratio",
			Observed:   0.42,
			Baseline:   0.001,
			Confidence: 0.9,
			Trajectory: "accelerating",
		},
		Impact: signal.Impact{
			Severity:    signal.Severity{DegradationPct: 0.42, Trajectory: "accelerating"},
			BlastRadius: signal.BlastRadius{AffectedPct: 0.35, Velocity: "accelerating", DownstreamConsumers: 2},
		},
	}

	fmt.Println(d.Divergence.Metric, d.Divergence.Observed, "vs baseline", d.Divergence.Baseline)
	fmt.Println("error budget burned:", d.Impact.Severity.DegradationPct)

	// Output:
	// cart_error_ratio 0.42 vs baseline 0.001
	// error budget burned: 0.42
}

// ExampleDivergence shows the first of the two confidence numbers this
// engine refuses to collapse into one field. This one answers "is the input
// trustworthy" and belongs to whatever detected the divergence; how sure a
// diagnosis is rides a proposal candidate instead, computed partly from
// this one.
func ExampleDivergence() {
	d := signal.Divergence{
		Metric:     "cart_error_ratio",
		Observed:   0.42,
		Baseline:   0.001,
		Confidence: 0.9,
		Trajectory: "accelerating",
	}

	fmt.Println("signal strength:", d.Confidence)

	// Output: signal strength: 0.9
}

// ExampleImpact shows the two axes kept apart on purpose: how far off
// objective the affected service drifted, and how many others are exposed.
// Collapsing them into one badness number loses the case where a small
// degradation reaches everyone.
func ExampleImpact() {
	narrow := signal.Impact{
		Severity:    signal.Severity{DegradationPct: 0.80},
		BlastRadius: signal.BlastRadius{AffectedPct: 0.05, DownstreamConsumers: 1},
	}
	wide := signal.Impact{
		Severity:    signal.Severity{DegradationPct: 0.10},
		BlastRadius: signal.BlastRadius{AffectedPct: 0.95, DownstreamConsumers: 12},
	}

	fmt.Println(narrow.Severity.DegradationPct, narrow.BlastRadius.AffectedPct)
	fmt.Println(wide.Severity.DegradationPct, wide.BlastRadius.AffectedPct)

	// Output:
	// 0.8 0.05
	// 0.1 0.95
}
