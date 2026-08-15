package clank

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// noopTopology is a placeholder for clank's real telemetry backend (Prometheus),
// used when unconfigured. Main's loop still runs; the proposal.SAO it assembles
// carries no live topology context until the real source is configured.
type noopTopology struct{}

func (noopTopology) Topology(context.Context, signal.Detection) (proposal.TopologySnapshot, error) {
	return proposal.TopologySnapshot{}, nil
}
