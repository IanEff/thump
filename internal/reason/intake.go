package reason

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// TopologySource resolves the upstream/downstream node state around a
// signal, for Assemble to snapshot into the SAO.
type TopologySource interface {
	Topology(ctx context.Context, sig signal.Detection) (proposal.TopologySnapshot, error)
}

// ChangeSource resolves recent change events near a signal — deploys, config
// changes — for Assemble to snapshot into the SAO. CausalScorerImpl.Score
// reads the result to weigh whether an event caused the signal.
type ChangeSource interface {
	Changes(ctx context.Context, sig signal.Detection) (proposal.ChangeSnapshot, error)
}
