package clank_test

import (
	"context"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
)

func TestIntake_AssemblesAVersionedSAO(t *testing.T) {
	t.Parallel()
	in := clank.NewIntake(fakeTopologySource(), fakeChangeSource())
	sao, err := in.Assemble(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}
	if sao.Version != 1 || len(sao.Change.Events) == 0 {
		t.Errorf("intake should assemble a v1 SAO with change events: %+v", sao)
	}
}

func sigBurnAccel() signal.Detection {
	return signal.Detection{
		Name:          "checkout-latency-burn-accel-001",
		Fingerprint:   "fp-checkout-latency-001",
		OriginService: "checkout",
		ServiceTier:   "tier-1",
		DetectorType:  "burn_rate_acceleration",
		Divergence:    signal.Divergence{Metric: "latency_p99", Observed: 850, Baseline: 200, Confidence: 0.9, Trajectory: "accelerating"},
		Impact: signal.Impact{
			// DegradationPct/AffectedPct are 0.0–1.0 fractions everywhere rattle
			// produces them (enrich.go/traffic.go clamp at 1.0); keep the fixture
			// on that scale so a prompt-content test never reads "severity 4000%".
			Severity:    signal.Severity{DegradationPct: 0.4, Trajectory: "accelerating"},
			BlastRadius: signal.BlastRadius{AffectedPct: 0.6, Velocity: "fast", DownstreamConsumers: 3},
		},
		DetectedAt: time.Now(),
	}
}

type fakeTopo struct {
	snap proposal.TopologySnapshot
	err  error
}

func (f fakeTopo) Topology(_ context.Context, _ signal.Detection) (proposal.TopologySnapshot, error) {
	return f.snap, f.err
}

type fakeChange struct {
	snap proposal.ChangeSnapshot
	err  error
}

func (f fakeChange) Changes(_ context.Context, _ signal.Detection) (proposal.ChangeSnapshot, error) {
	return f.snap, f.err
}

func fakeTopologySource() clank.TopologySource {
	return fakeTopo{}
}

func fakeChangeSource() clank.ChangeSource {
	return fakeChange{snap: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
		{ID: "deploy-7f3a", Type: "deploy", Target: "checkout", Age: 12 * time.Minute},
	}}}
}
