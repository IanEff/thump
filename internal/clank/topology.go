package clank

import (
	"context"

	"github.com/ianeff/thump/internal/proposal"
	"github.com/ianeff/thump/internal/signal"
	"github.com/ianeff/thump/internal/whir"
)

type WhirTopology struct {
	Catalog  whir.Catalog
	Resolver *whir.Resolver
}

func (w WhirTopology) Topology(ctx context.Context, sig signal.Detection) (TopologySnapshot, error) {
	var snap TopologySnapshot
	for _, dep := range w.Catalog.Edges(sig.OriginService) {
		snap.Upstream = append(snap.Upstream, proposal.NodeState{
			Name:  dep,
			State: w.Resolver.State(ctx, dep),
		})
	}
	return snap, nil
}
