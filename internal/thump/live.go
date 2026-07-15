package thump

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

// ActionRunner performs one catalogued action against real infrastructure —
// the forward action for a contract (reverse=false) or its authored undo
// (reverse=true). It is the only seam through which thump can touch the world,
// and it is deliberately expressed in primitives, not an Order: every concrete
// implementation lives outside package thump and is injected, so the dry-run
// import allowlist keeps meaning something. The runner owns the ref -> real
// command binding — it is the one place that knows what
// "throttle-non-critical-paths" runs to act and what "unthrottle" runs to undo.
type ActionRunner interface {
	Run(ctx context.Context, ref string, reverse bool, params map[string]float64) error
}

// Live is the Executor that actually acts — it delegates to an ActionRunner
// and records what happened, never rendering-and-stopping the way DryRun does.
// It reaches infrastructure only through the injected Runner, so package thump
// itself stays clean of os/exec and clients; a nil Runner is a programming
// error, not a runtime fallback.
type Live struct {
	Runner ActionRunner
}

// Execute runs o's action (its undo when o is a reversal) and reports the
// result as a live Outcome: ResultSuccess when the runner returns nil, or
// ResultFailure carrying the runner's error text when it does not — a failure
// with no error text is silence, not accountability, so the text is required
// company, not decoration.
func (l Live) Execute(ctx context.Context, o Order, now time.Time) outcome.Outcome {
	oc := outcome.Outcome{
		ID:          fmt.Sprintf("out:%s:%d", o.SignalRef, now.Unix()),
		DecisionRef: o.DecisionRef,
		SignalRef:   o.SignalRef,
		ContractRef: o.ContractRef,
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultSuccess,
		ExecutedAt:  now,
	}
	if err := l.Runner.Run(ctx, o.ContractRef, o.Kind == OrderReversal, o.Parameters); err != nil {
		oc.Result = outcome.ResultFailure
		oc.Error = err.Error()
	}
	return oc
}
