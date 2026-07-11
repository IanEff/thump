package rattle

import (
	"context"
	"log/slog"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/publish"
	"go.opentelemetry.io/otel/trace/noop"
)

func RunLoopForTest(ctx context.Context, r *Reconciler, log *slog.Logger, pub publish.Publisher[signal.Detection]) {
	runLoop(ctx, r, log, pub, noop.Tracer{})
}

// NewReconcilerForTest exposes Main's real Reconciler assembly so a test can
// swap in a fake Source and prove Main's wiring, not just Reconciler's
// behavior when a test hand-sets a field.
func NewReconcilerForTest(promURL string, slos []SLO, topo TopologySource, traffic TrafficSource) *Reconciler {
	return newReconciler(promURL, slos, topo, traffic)
}
