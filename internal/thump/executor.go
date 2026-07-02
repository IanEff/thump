package thump

import (
	"context"
	"fmt"
	"time"
)

type Executor interface {
	Execute(ctx context.Context, o Order, now time.Time) Outcome
}

type DryRun struct{}

func (DryRun) Execute(_ context.Context, o Order, now time.Time) Outcome {
	return Outcome{
		ID:          fmt.Sprintf("out:%s:%d", o.SignalRef, now.Unix()),
		DecisionRef: o.DecisionRef,
		SignalRef:   o.SignalRef,
		ContractRef: o.ContractRef,
		Mode:        ModeDryRun,
		Result:      ResultRendered,
		ExecutedAt:  now,
	}
}
