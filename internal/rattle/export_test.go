package rattle

import (
	"context"
	"log/slog"
)

// LoadSLOsForTest exposes the compiled-in watch list to rattle_test without
// loadSLOs becoming part of rattle's real API. Only compiled under `go test`
// — the _test.go suffix keeps it out of the shipped binary. Mirrors
// internal/clank/export_test.go.
func LoadSLOsForTest() []SLO { return loadSLOs() }

func RunLoopForTest(ctx context.Context, r *Reconciler, log *slog.Logger, sink DetectionSink) {
	runLoop(ctx, r, log, sink)
}

// NewReconcilerForTest exposes Main's real Reconciler assembly so a test can
// swap in a fake Source and prove Main's wiring, not just Reconciler's
// behavior when a test hand-sets a field.
func NewReconcilerForTest(promURL string, topo TopologySource, traffic TrafficSource) *Reconciler {
	return newReconciler(promURL, topo, traffic)
}
