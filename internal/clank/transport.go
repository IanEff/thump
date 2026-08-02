package clank

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/beat"
)

// Transport is clank's directory-poll ingestion path: it globs
// signal.Detection YAML files out of Inbox, runs each through Engine.Propose,
// and disposes of the file — processed, quarantined (unparseable), or
// stalled (Propose kept failing). It is the keyless fake transport the seam
// tests drive; runBroker's NATS path is how a real deployment ingests.
type Transport struct {
	Inbox  string
	Engine *Engine

	// MaxProposeAttempts is how many failed Propose calls a detection gets
	// before it's filed stalled instead of retried; zero means
	// maxProposeAttempts.
	MaxProposeAttempts int

	attempts map[string]int
}

const maxProposeAttempts = 5 // a detection whose Propose call fails this many times is filed stalled, not retried forever

// Tick processes every detection file currently in Inbox once. A file that
// fails to unmarshal is quarantined immediately — poison doesn't block the
// queue. A file whose Propose call errors is left for the next Tick to
// retry, up to MaxProposeAttempts, then filed stalled. A file that reasons
// successfully — gated or not — is filed processed.
func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return beat.DrainDir(tr.Inbox, "clank", func(path string, det signal.Detection) error {
		_, err := tr.Engine.Propose(ctx, det)
		if err != nil {
			maxAttempts := tr.MaxProposeAttempts
			if maxAttempts <= 0 {
				maxAttempts = maxProposeAttempts
			}
			if tr.attempts == nil {
				tr.attempts = make(map[string]int)
			}
			tr.attempts[path]++
			if tr.attempts[path] >= maxAttempts {
				slog.Error("giving up on detection", "path", path, "attempts", tr.attempts[path], "err", err)
				delete(tr.attempts, path)
				if dErr := beat.Disposition(tr.Inbox, path, "stalled"); dErr != nil {
					return fmt.Errorf("clank: stall %s: %w", path, dErr)
				}
				return nil
			}
			slog.Warn("propose failed, will retry", "path", path, "attempts", tr.attempts[path], "err", err)
			return nil
		}
		delete(tr.attempts, path)

		if err := beat.Disposition(tr.Inbox, path, "processed"); err != nil {
			return fmt.Errorf("clank: archive %s: %w", path, err)
		}
		return nil
	})
}
