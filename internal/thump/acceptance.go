package thump

import (
	"context"
	"time"
)

// ReleaseProbe answers whether the open release named by key was ever
// merged — the acceptance-poll seam for a release-mode Order. Its signature
// matches actuate.Forge.Withdraw exactly, so the real forge satisfies it
// with no import of internal/actuate needed here, the same way Converger
// takes no dependency on internal/converge.
type ReleaseProbe interface {
	Withdraw(ctx context.Context, key string) (accepted bool, err error)
}

// AcceptanceWatcher polls whether a release-mode forward Order was ever
// merged, once its authored success window elapses — release mode's
// precondition for ReversalWatcher, not a variant of it: a release nobody
// accepted was never applied, so there is nothing yet to converge.
//
// The wait reuses Order.Success.Window rather than a second authored
// duration — a deliberate reuse, not a modeling accident. The accepted path
// therefore waits Success.Window twice in a row before a convergence
// Outcome exists: once here, once again inside ReversalWatcher.Watch.
type AcceptanceWatcher struct {
	Probe ReleaseProbe
}

// Poll blocks for o's success Window, then withdraws the forward release
// named by o.ContractRef — a release's forward key is its ref alone, so
// internal/thump never needs actuate's own key-derivation to ask about it.
// A cancelled ctx polls nothing.
func (w AcceptanceWatcher) Poll(ctx context.Context, o Order) (accepted bool, err error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(o.Success.Window):
	}
	return w.Probe.Withdraw(ctx, o.ContractRef)
}
