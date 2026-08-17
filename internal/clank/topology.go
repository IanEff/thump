package clank

import (
	"context"
	"slices"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/whir"
)

// WhirTopology is the real reason.TopologySource: it resolves a signal's upstream
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
	return proposal.TopologySnapshot{
		Upstream: catalogUpstream(ctx, w.Catalog, w.Resolver, sig.OriginService),
	}, nil
}

// ObservedTopology prefers the graph the service-graph connector observed and
// falls back to whir's authored catalog for a service that emits no traces.
// Both sides fill Downstream, which the authored catalog alone cannot: whir's
// edges are declared one-directional (internal/clank/topology.go:22-24).
//
// An observed edge's State is taken exactly as the connector reported it —
// never overwritten by Resolver. Resolver answers "is the pod up" (a replica
// count); the connector answers "is it erroring" (a request failure ratio).
// Those are different questions, and a service can fail one while passing the
// other — a flagd config flip degrades cart's calls to it without ever
// touching flagd's replica count. Resolver only stands in for a service the
// graph observed nothing about at all.
type ObservedTopology struct {
	Graph    *whir.GraphSource
	Fallback WhirTopology
}

// Topology returns the dependency snapshot for sig.OriginService — preferring observed
// service graph traces with TrafficShare and falling back to the authored catalog when unobserved.
func (o ObservedTopology) Topology(ctx context.Context, sig signal.Detection) (proposal.TopologySnapshot, error) {
	var snap proposal.TopologySnapshot

	if o.Graph != nil {
		if edges, err := o.Graph.Edges(ctx, sig.OriginService); err == nil && len(edges) > 0 {
			snap.Upstream = observedNodeStates(edges, func(e whir.Edge) string { return e.Server })
		}
	}
	if snap.Upstream == nil {
		snap.Upstream = catalogUpstream(ctx, o.Fallback.Catalog, o.Fallback.Resolver, sig.OriginService)
	}

	if o.Graph != nil {
		if edges, err := o.Graph.Inbound(ctx, sig.OriginService); err == nil && len(edges) > 0 {
			snap.Downstream = observedNodeStates(edges, func(e whir.Edge) string { return e.Client })
		}
	}
	if snap.Downstream == nil {
		snap.Downstream = catalogDownstream(ctx, o.Fallback.Catalog, o.Fallback.Resolver, sig.OriginService)
	}

	return snap, nil
}

// observedNodeStates converts service-graph edges into NodeStates, reading
// each edge's State and Share as the connector reported them. name picks
// which side of the edge is the other node — edge.Server for an outbound
// edge, edge.Client for an inbound one.
func observedNodeStates(edges []whir.Edge, name func(whir.Edge) string) []proposal.NodeState {
	var states []proposal.NodeState
	for _, edge := range edges {
		states = append(states, proposal.NodeState{
			Name:         name(edge),
			State:        edge.State,
			TrafficShare: edge.Share,
		})
	}
	return states
}

// catalogUpstream resolves origin's declared dependencies from catalog,
// asking resolver for each one's live state. It is WhirTopology's only path
// and ObservedTopology's fallback for a service the graph observed nothing about.
func catalogUpstream(ctx context.Context, catalog whir.Catalog, resolver *whir.Resolver, origin string) []proposal.NodeState {
	var states []proposal.NodeState
	for _, dep := range catalog.Edges(origin) {
		states = append(states, proposal.NodeState{Name: dep, State: resolveState(ctx, resolver, dep)})
	}
	return states
}

// catalogDownstream inverts catalog's declared dependencies to find who
// depends on origin — ObservedTopology's fallback for a service the graph
// observed no inbound callers for.
func catalogDownstream(ctx context.Context, catalog whir.Catalog, resolver *whir.Resolver, origin string) []proposal.NodeState {
	var states []proposal.NodeState
	for _, entity := range catalog.Entities {
		if entity.Name != origin && slices.Contains(entity.DependsOn, origin) {
			states = append(states, proposal.NodeState{Name: entity.Name, State: resolveState(ctx, resolver, entity.Name)})
		}
	}
	return states
}

func resolveState(ctx context.Context, resolver *whir.Resolver, dependency string) string {
	if resolver == nil {
		return whir.StateUnknown
	}
	return resolver.State(ctx, dependency)
}
