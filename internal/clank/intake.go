package clank

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/signal"
)

var (
	ErrTopologySource = errors.New("intake: topology source")
	ErrChangeSoure    = errors.New("intake: change source")
)

type TopologySource interface {
	Topology(ctx context.Context, sig signal.Detection) (TopologySnapshot, error)
}

type ChangeSource interface {
	Changes(ctx context.Context, sig signal.Detection) (ChangeSnapshot, error)
}

type Intake struct {
	topo   TopologySource
	change ChangeSource
}

func NewIntake(topo TopologySource, change ChangeSource) *Intake {
	return &Intake{topo: topo, change: change}
}

func (in *Intake) Assemble(ctx context.Context, sig signal.Detection) (SAO, error) {
	topo, err := in.topo.Topology(ctx, sig)
	if err != nil {
		return SAO{}, fmt.Errorf("%w: %w", ErrTopologySource, err)
	}
	change, err := in.change.Changes(ctx, sig)
	if err != nil {
		return SAO{}, fmt.Errorf("%w: %w", ErrChangeSoure, err)
	}

	return SAO{
		Version:     1,
		AssembledAt: time.Now(),
		Signal: SignalSnapshot{
			Confidence:  sig.Divergence.Confidence,
			Metric:      sig.Divergence.Metric,
			Severity:    sig.Impact.Severity,
			BlastRadius: sig.Impact.BlastRadius,
		},
		Topology: topo,
		Change:   change,
	}, nil
}
