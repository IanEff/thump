package clank

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/whir"
)

// WhirTopology is the real TopologySource: it resolves a signal's upstream
// dependencies from whir's static catalog-info.yaml graph, then asks the
// Resolver for each dependency's live state. Main wires it in only when
// WHIR_CATALOG and WHIR_STATE_QUERIES are both set; otherwise Main falls back
// to a topology-less noop.
type WhirTopology struct {
	Catalog  whir.Catalog
	Resolver *whir.Resolver
}

// Topology returns one proposal.NodeState per upstream dependency whir's
// catalog names for sig.OriginService. Downstream is never populated here —
// whir's edges are declared one-directional, a service names what it
// depends on, not who depends on it.
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
