package rattle

import (
	"context"

	"github.com/ianeff/thump/internal/whir"
)

type WhirTopologySource struct {
	Resolver *whir.Resolver
}

func (w *WhirTopologySource) DependencyState(ctx context.Context, dep Dependency) (string, error) {
	return w.Resolver.State(ctx, dep.Name), nil
}
