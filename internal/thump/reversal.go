package thump

import (
	"context"
	"fmt"
	"time"
)

// Converger reports whether an Order's SuccessCriteria are met — the metric
// probe an automatic reversal consults before it fires. The live
// implementation reads real telemetry; until it exists, nothing outside a
// test satisfies this.
type Converger interface {
	Converged(ctx context.Context, o Order) bool
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

// Watch blocks for o's success Window, then returns the reversal Order if o
// still hasn't converged, or (Order{}, false) if it has — a cancelled ctx
// fires nothing.
func (w ReversalWatcher) Watch(ctx context.Context, o Order) (Order, bool) {
	select {
	case <-ctx.Done():
		return Order{}, false
	case <-time.After(o.Success.Window):
	}
	if w.Probe.Converged(ctx, o) {
		return Order{}, false
	}
	return reversalOf(o, w.now()), true
}

func (w ReversalWatcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
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
