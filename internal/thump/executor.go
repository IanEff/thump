package thump

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

// Executor performs one Order and reports what happened. v1 ships exactly
// one implementation, DryRun — no live executor exists yet, so nothing that
// touches infrastructure can be reached through this seam.
type Executor interface {
	// Execute carries out o and returns the resulting outcome.Outcome. now
	// is passed in rather than read from time.Now inside, so callers can
	// freeze it for a deterministic Outcome.ExecutedAt.
	Execute(ctx context.Context, o Order, now time.Time) outcome.Outcome
}

// DryRun is the only Executor v1 ships: it never calls os/exec, net, or a
// Kubernetes client — a fact enforced by an import-allowlist test on this
// package, not merely by DryRun's own behavior.
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
