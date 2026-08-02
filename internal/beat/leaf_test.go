package beat_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestBeatImportsNoBeat pins the kit's load-bearing invariant: internal/beat
// may import stdlib, the shared transport infrastructure (broker, publish,
// and the jetstream types they surface), health (consumer.go's BrokerHooks
// and AwaitConsumers take a *health.Health, and metrics.go's Metrics builds
// one), the OTel trace API (stage.go's Stage takes a trace.Tracer and marks
// span status through otel/codes — the tracer itself is built elsewhere, by
// internal/otelx, and handed in), the Prometheus client (metrics.go's
// Metrics, stage.go's StageRecorder), and sigs.k8s.io/yaml (drain.go's
// DrainDir, which every beat's dir-poll Tick unmarshals its inbox files
// through) — but NEVER a beat package.
// A clank, rattle, hiss, or thump import appearing here means the runtime
// kit has become a place where the planes mash together; this test is that
// regression's tripwire. Widen the allowlist below when the kit grows a new
// dependency; never widen it with a beat import.
func TestBeatImportsNoBeat(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		leaftest.Stdlib,
		"github.com/ianeff/thump/internal/broker",
		"github.com/ianeff/thump/internal/health",
		"github.com/ianeff/thump/internal/publish",
		"github.com/nats-io/nats.go/jetstream",
		"github.com/prometheus/client_golang/prometheus",
		"github.com/prometheus/client_golang/prometheus/promhttp",
		"go.opentelemetry.io/otel/codes",
		"go.opentelemetry.io/otel/trace",
		"sigs.k8s.io/yaml",
	)
}
