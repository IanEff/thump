package harvest

import (
	"context"

	"github.com/ianeff/thump/api/v1/proposal"
)

// SetWatcher is how a harvest learns what the engine concluded, vs
// Watcher's what happened.
type SetWatcher interface {
	Sets(ctx context.Context) (<-chan proposal.Set, error)
}

// firstSetFor returns the first Set published for signalRef, or
// false if the channel closes first.
func firstSetFor(ctx context.Context, w SetWatcher, signalRef string) (proposal.Set, bool) {
	ch, err := w.Sets(ctx)
	if err != nil {
		return proposal.Set{}, false
	}
	for {
		select {
		case <-ctx.Done():
			return proposal.Set{}, false
		case s, ok := <-ch:
			if !ok {
				return proposal.Set{}, false
			}
			if s.SignalRef == signalRef {
				return s, true
			}
		}
	}
}
