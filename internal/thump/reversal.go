package thump

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/internal/beat"
)

// Converger reports both the reversal verdict and the normalized post-action
// severity for an Order — the two facts watchAndSettle needs after the
// window. The live implementation reads real telemetry; until it exists,
// nothing outside a test satisfies this.
type Converger interface {
	Settle(ctx context.Context, o Order) (converged bool, severity *float64)
}

// ReversalWatcher fires the authored undo when a forward Order's success
// window elapses with its criteria unmet. The reversal rides the original
// approval — no fresh governance pass — because reversal.method was part of
// the ActionContract hiss already granted, so the undo is the second half of
// one governed transaction, not a new one.
type ReversalWatcher struct {
	Probe Converger        // the convergence check run once the window elapses
	Now   func() time.Time // overridable clock for the reversal Order's timestamp; nil means time.Now
}

// Settlement is what one post-window probe read decided: whether the success
// criteria were met, and the undo Order to run — a restore after a win or a
// reversal after a loss. Converged and Fire are separate fields because a
// restore is a success: collapsing them records a met window as a
// non-convergence.
type Settlement struct {
	Converged bool     // the terminal outcome's input — success vs partial_non_converging
	Fire      bool     // whether Undo runs at all; true on non-convergence, or on a win the contract authored a restore for
	Undo      Order    // the reversal Order, zero when Fire is false
	Severity  *float64 // nil stays nil — unmeasured never becomes a fabricated 0.0
}

// Watch blocks for o's success Window, then reports what one post-window
// probe read decided. Fire is true on a loss whatever o.Reversal declares,
// and on a win only when o.Reversal.RestoreOnSuccess is authored true — a
// cancelled ctx fires nothing.
func (w ReversalWatcher) Watch(ctx context.Context, o Order) Settlement {
	select {
	case <-ctx.Done():
		return Settlement{}
	case <-time.After(o.Success.Window):
	}
	converged, severity := w.Probe.Settle(ctx, o)
	fire := !converged || o.Reversal.RestoreOnSuccess
	if !fire {
		return Settlement{Converged: converged, Severity: severity}
	}
	return Settlement{Converged: converged, Fire: true, Undo: reversalOf(o, w.now()), Severity: severity}
}

func (w ReversalWatcher) now() time.Time {
	return beat.Clock(w.Now)()
}

// reversalOf renders the undo of a forward Order: it inherits the same grant
// and signal, executes the forward action's authored reversal.method, and
// carries OrderReversal so a kill-switch exempts it from any disarm.
func reversalOf(o Order, now time.Time) Order {
	return Order{
		ID:          fmt.Sprintf("rev:%s:%d", o.SignalRef, now.Unix()),
		Kind:        OrderReversal,
		DecisionRef: o.DecisionRef,
		SignalRef:   o.SignalRef,
		ContractRef: o.ContractRef,
		Description: o.Reversal.Method,
		Reversal:    o.Reversal,
		RenderedAt:  now,
	}
}
