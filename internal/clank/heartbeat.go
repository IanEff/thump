package clank

import "context"

// heartbeatKey is unexported so WithHeartbeat is the only way to set it —
// no other package can forge a ctx value that satisfies heartbeatFrom.
type heartbeatKey struct{}

// WithHeartbeat returns a ctx carrying fn as the per-run liveness signal
// HeartbeatingStore calls after every successful checkpoint. runBroker's
// detection handler is the only production caller — it hands in a
// msg.InProgress closure so a slow-but-alive reason loop keeps its
// JetStream delivery from looking indistinguishable from a dead one,
// without engine.go's loop ever needing to know a JetStream message exists.
func WithHeartbeat(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, heartbeatKey{}, fn)
}

// heartbeatFrom returns the func WithHeartbeat attached to ctx, or nil if
// there isn't one — the common case outside runBroker (tests, the offline
// dir-poll transport), where a checkpoint has nothing to ping.
func heartbeatFrom(ctx context.Context) func() {
	fn, _ := ctx.Value(heartbeatKey{}).(func())
	return fn
}

// HeartbeatingStore decorates a Store, calling ctx's heartbeat (if any —
// see WithHeartbeat) after each successful Checkpoint. It pings on real,
// synchronous progress rather than a wall clock: a reason loop that's
// actually hung — not just slow — never checkpoints again, so it stops
// heartbeating and a stalled JetStream delivery still gets redelivered
// instead of looking perpetually alive.
type HeartbeatingStore struct {
	Store
}

// Checkpoint delegates to the wrapped Store, then calls ctx's heartbeat —
// skipped entirely if Checkpoint errors, so a failed write never reports
// false progress.
func (h HeartbeatingStore) Checkpoint(ctx context.Context, t Turn) error {
	if err := h.Store.Checkpoint(ctx, t); err != nil {
		return err
	}
	if hb := heartbeatFrom(ctx); hb != nil {
		hb()
	}
	return nil
}
