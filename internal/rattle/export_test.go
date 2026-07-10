package rattle

import (
	"context"
	"log/slog"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/publish"
	"go.opentelemetry.io/otel/trace/noop"
)

// LoadSLOsForTest exposes the compiled-in watch list to rattle_test without
// loadSLOs becoming part of rattle's real API. Only compiled under `go test`
// — the _test.go suffix keeps it out of the shipped binary. Mirrors
// internal/clank/export_test.go.
func LoadSLOsForTest() []SLO { return loadSLOs() }

func RunLoopForTest(ctx context.Context, r *Reconciler, log *slog.Logger, pub publish.Publisher[signal.Detection]) {
	runLoop(ctx, r, log, pub, noop.Tracer{})
}

// NewReconcilerForTest exposes Main's real Reconciler assembly so a test can
// swap in a fake Source and prove Main's wiring, not just Reconciler's
// behavior when a test hand-sets a field.
func NewReconcilerForTest(promURL string, topo TopologySource, traffic TrafficSource) *Reconciler {
	return newReconciler(promURL, topo, traffic)
}
