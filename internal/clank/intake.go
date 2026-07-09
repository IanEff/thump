package clank

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

var (
	ErrTopologySource = errors.New("intake: topology source")
	ErrChangeSoure    = errors.New("intake: change source")
)

type TopologySource interface {
	Topology(ctx context.Context, sig signal.Detection) (proposal.TopologySnapshot, error)
}

type ChangeSource interface {
	Changes(ctx context.Context, sig signal.Detection) (proposal.ChangeSnapshot, error)
}

type Intake struct {
	topo   TopologySource
	change ChangeSource
}

func NewIntake(topo TopologySource, change ChangeSource) *Intake {
	return &Intake{topo: topo, change: change}
}

func (in *Intake) Assemble(ctx context.Context, sig signal.Detection) (proposal.SAO, error) {
	topo, err := in.topo.Topology(ctx, sig)
	if err != nil {
		return proposal.SAO{}, fmt.Errorf("%w: %w", ErrTopologySource, err)
	}
	if len(topo.Upstream) == 0 && len(topo.Downstream) == 0 {
		// The pluggable TopologySource (WhirTopology, or noop until it's wired)
		// has nothing to say — fall back to what rattle already observed on
		// the Detection itself, rather than silently dropping it. This isn't
		// new source wiring; sig.Topology is data clank already has in hand.
		topo = topologyFromSignal(sig.Topology)
	}
	change, err := in.change.Changes(ctx, sig)
	if err != nil {
		return proposal.SAO{}, fmt.Errorf("%w: %w", ErrChangeSoure, err)
	}

	return proposal.SAO{
		Version:     1,
		AssembledAt: time.Now(),
		Signal: proposal.SignalSnapshot{
			Confidence:  sig.Divergence.Confidence,
			Metric:      sig.Divergence.Metric,
			Severity:    sig.Impact.Severity,
			BlastRadius: sig.Impact.BlastRadius,
		},
		Topology: topo,
		Change:   change,
	}, nil
}

// topologyFromSignal adapts rattle's TopologyContext (signal.ObservedNode:
// Service + State) onto clank's own proposal.TopologySnapshot (proposal.NodeState) — the two
// shapes exist independently because clank's proposal.NodeState carries fields
// (DegradedSince, TrafficShare) rattle's ObservedNode doesn't have yet.
func topologyFromSignal(t signal.TopologyContext) proposal.TopologySnapshot {
	var snap proposal.TopologySnapshot
	for _, n := range t.Upstream {
		snap.Upstream = append(snap.Upstream, proposal.NodeState{Name: n.Service, State: n.State})
	}
	for _, n := range t.Downstream {
		snap.Downstream = append(snap.Downstream, proposal.NodeState{Name: n.Service, State: n.State})
	}
	return snap
}
