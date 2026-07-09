package clank

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// noopTopology and noopChange are placeholders for clank's real telemetry /
// change backends (Prometheus, ArgoCD), still deferred. They let Main's loop
// run today; the proposal.SAO it assembles just carries no live topology / change
// context until the real sources land.
type noopTopology struct{}

func (noopTopology) Topology(context.Context, signal.Detection) (proposal.TopologySnapshot, error) {
	return proposal.TopologySnapshot{}, nil
}

type noopChange struct{}

func (noopChange) Changes(context.Context, signal.Detection) (proposal.ChangeSnapshot, error) {
	return proposal.ChangeSnapshot{}, nil
}
