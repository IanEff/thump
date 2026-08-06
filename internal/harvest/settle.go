package harvest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

// ErrSettleTimeout means the settle window elapsed with no terminal
// outcome on the fingerprint.
var ErrSettleTimeout = errors.New("harvest: settle window elapsed with no terminal outcome")

// Watcher is how a harvest learns an incident finished.  Production
// satisfies it by consuming thump.outcomes; test satisfies it with a
// channel.
type Watcher interface {
	Outcomes(ctx context.Context) (<-chan outcome.Outcome, error)
}

// isTerminal names the settled results explicitly rather than excluding
// ResultApplied — a zero-valued Result is not a settled one, and an
// exclusion test reads it as terminal and ends the wait on a record that
// says nothing.
func isTerminal(r outcome.Result) bool {
	switch r {
	case outcome.ResultSuccess,
		outcome.ResultFailure,
		outcome.ResultPartialNonConverging,
		outcome.ResultBlocked,
		outcome.ResultUnknown,
		outcome.ResultRendered:
		return true
	default:
		return false
	}
}

// Settle blocks until w reports a terminal outcome for signalRef, or
// window elapses.
func Settle(ctx context.Context, w Watcher, signalRef string, window time.Duration) (outcome.Outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	ch, err := w.Outcomes(ctx)
	if err != nil {
		return outcome.Outcome{}, fmt.Errorf("harvest: watch %s: %w", signalRef, err)
	}

	for {
		select {
		case <-ctx.Done():
			return outcome.Outcome{}, fmt.Errorf("%w: %s", ErrSettleTimeout, signalRef)
		case o, ok := <-ch:
			if !ok {
				return outcome.Outcome{}, fmt.Errorf("%w: %s", ErrSettleTimeout, signalRef)
			}
			if o.SignalRef != signalRef || !isTerminal(o.Result) {
				continue
			}
			return o, nil
		}
	}
}
