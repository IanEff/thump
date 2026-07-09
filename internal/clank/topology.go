package clank

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/whir"
)

type WhirTopology struct {
	Catalog  whir.Catalog
	Resolver *whir.Resolver
}

func (w WhirTopology) Topology(ctx context.Context, sig signal.Detection) (proposal.TopologySnapshot, error) {
	var snap proposal.TopologySnapshot
	for _, dep := range w.Catalog.Edges(sig.OriginService) {
		snap.Upstream = append(snap.Upstream, proposal.NodeState{
			Name:  dep,
			State: w.Resolver.State(ctx, dep),
		})
	}
	return snap, nil
}
