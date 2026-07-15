package thump

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

// KillSwitch reports whether Live actuation is armed right now — one coarse,
// global trust state for the whole execution subsystem, not a per-order
// verdict. It only ever subtracts authority hiss already granted; it never
// grants, so it is a circuit breaker, not a second governor. hiss answers "is
// this order allowed"; the switch answers "do we trust anything Live at all".
type KillSwitch interface {
	Armed(ctx context.Context) bool
}

// GatedExecutor fronts a real Executor and refuses to run a forward Order
// while the KillSwitch is disarmed — meant to wrap a Live executor, since
// gating DryRun would only rehearse a refusal. A reversal Order is exempt:
// blocking cleanup mid-flight strands infrastructure half-changed, a worse
// failure than letting one bounded, already-approved undo finish.
type GatedExecutor struct {
	Inner  Executor
	Switch KillSwitch
}

// Execute delegates to Inner for any reversal, and for a forward Order only
// when the switch is armed — otherwise it records a blocked Outcome and never
// reaches Inner. The switch is read exactly once, before the call, never
// mid-flight.
func (g GatedExecutor) Execute(ctx context.Context, o Order, now time.Time) outcome.Outcome {
	if o.Kind != OrderReversal && !g.Switch.Armed(ctx) {
		return blockedOutcome(o, now)
	}
	return g.Inner.Execute(ctx, o, now)
}

// blockedOutcome records a kill-switch refusal on the live path — Mode is
// live because the block is a fact about real execution, not a rehearsal, and
// Result is blocked, which Auditable accepts with no error text since a
// refusal is not a failure.
func blockedOutcome(o Order, now time.Time) outcome.Outcome {
	return outcome.Outcome{
		ID:          fmt.Sprintf("out:%s:%d", o.SignalRef, now.Unix()),
		DecisionRef: o.DecisionRef,
		SignalRef:   o.SignalRef,
		ContractRef: o.ContractRef,
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultBlocked,
		ExecutedAt:  now,
	}
}
