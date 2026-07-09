package rattle

import (
	"context"

	"github.com/ianeff/thump/internal/whir"
)

// WhirTopologySource adapts a whir.Resolver to TopologySource — the live
// topology backend Reconciler queries when WHIR_CATALOG and
// WHIR_STATE_QUERIES are both configured (see rattle.go's Main).
type WhirTopologySource struct {
	Resolver *whir.Resolver
}

// DependencyState reports the resolver's current state for dep. The error
// return exists only to satisfy TopologySource — whir.Resolver.State cannot
// itself fail; an unqueryable dependency resolves to whir.StateUnknown, not
// an error.
func (w *WhirTopologySource) DependencyState(ctx context.Context, dep Dependency) (string, error) {
	return w.Resolver.State(ctx, dep.Name), nil
}
