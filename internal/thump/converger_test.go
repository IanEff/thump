package thump_test

import (
	"context"
	"testing"

	"github.com/ianeff/thump/internal/thump"
)

type fakeProbe struct {
	answer            bool
	gotMetric, gotTgt string
}

func (f *fakeProbe) Converged(_ context.Context, metric, target string) bool {
	f.gotMetric, f.gotTgt = metric, target
	return f.answer
}

func TestPrometheusConverger_UnpacksOrderSuccessIntoTheProbe(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{answer: true}
	c := thump.PrometheusConverger{Probe: probe}

	got := c.Converged(context.Background(), goldenOrder())

	if !got {
		t.Error("PrometheusConverger must return the probe's own answer")
	}
	if probe.gotMetric != "latency_p99" || probe.gotTgt != "p99 < 250ms" {
		t.Errorf("probe got (%q, %q), want the order's Success.Metric/.Target", probe.gotMetric, probe.gotTgt)
	}
}
