package thump

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

type Executor interface {
	Execute(ctx context.Context, o Order, now time.Time) outcome.Outcome
}

type DryRun struct{}

func (DryRun) Execute(_ context.Context, o Order, now time.Time) outcome.Outcome {
	return outcome.Outcome{
		ID:          fmt.Sprintf("out:%s:%d", o.SignalRef, now.Unix()),
		DecisionRef: o.DecisionRef,
		SignalRef:   o.SignalRef,
		ContractRef: o.ContractRef,
		Mode:        outcome.ModeDryRun,
		Result:      outcome.ResultRendered,
		ExecutedAt:  now,
	}
}
