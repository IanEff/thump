package rattle

import (
	"context"
	"crypto/tls"
	"log/slog"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/publish"
	"go.opentelemetry.io/otel/trace/noop"
)

func RunLoopForTest(ctx context.Context, r *Reconciler, log *slog.Logger, pub publish.Publisher[signal.Detection]) {
	runLoop(ctx, r, log, pub, noop.Tracer{}, nil)
}

// NewReconcilerForTest exposes Main's real Reconciler assembly so a test can
// swap in a fake Source and prove Main's wiring, not just Reconciler's
// behavior when a test hand-sets a field.
func NewReconcilerForTest(promURL string, slos []SLO, topo TopologySource, traffic TrafficSource, backendTLS *tls.Config) *Reconciler {
	return newReconciler(promURL, slos, topo, traffic, backendTLS, QueryConfig{Step: time.Minute, Window: 15 * time.Minute})
}

// BuildSourcesForTest exposes buildSources to rattle_test.
func BuildSourcesForTest(cfg config.Rattle, backendTLS *tls.Config) (TopologySource, TrafficSource, error) {
	return buildSources(cfg, backendTLS)
}
