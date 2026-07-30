package thump

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

// Executor performs one Order and reports what happened. DryRun only ever
// renders; Live (live.go) actually acts, but solely through an injected
// ActionRunner — this package still never calls os/exec, net, or a Kubernetes
// client directly, a boundary an import-allowlist test enforces rather than
// trusting either implementation's own behavior.
type Executor interface {
	// Execute carries out o and returns the resulting outcome.Outcome. now
	// is passed in rather than read from time.Now inside, so callers can
	// freeze it for a deterministic Outcome.ExecutedAt.
	Execute(ctx context.Context, o Order, now time.Time) outcome.Outcome
}

// DryRun is the default Executor, and the only one buildExecutor returns
// unless THUMP_EXECUTOR is "live": it renders o and stops, touching nothing.
type DryRun struct{}

// Execute always reports outcome.ModeDryRun / outcome.ResultRendered — v1's
// ceiling is "thump knows what it would have done," never "thump did it".
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
