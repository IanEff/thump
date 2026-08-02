package clank

import (
	"context"
	"errors"
	"fmt"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/beat"
)

// DeclineEdge is clank's dir-poll consumer for governance's non-approvals —
// thump.declines' offline twin. It never touches Click or the case base;
// its only job is closing the ledger's dedup window the moment hiss rules
// against a Set, rather than waiting out the full DedupeWindow.
type DeclineEdge struct {
	Inbox  string
	Ledger *MemProposalLog
}

// Tick processes every decision.Decision file currently in Inbox once: a
// file that fails to unmarshal is quarantined; one Decline accepts is filed
// processed; one with no open set to answer to (ErrNoOpenSet) is filed
// unmatched, not an error — the set may have already closed for another
// reason. Mirrors ReturnEdge.Tick's exact disposition shape.
func (de *DeclineEdge) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return beat.DrainDir(de.Inbox, "decline", func(path string, dec decision.Decision) error {
		switch _, err := de.Ledger.Decline(ctx, dec.SignalRef, dec.EvaluatedAt); {
		case err == nil:
			if pErr := beat.Disposition(de.Inbox, path, "processed"); pErr != nil {
				return fmt.Errorf("decline: archive %s: %w", path, pErr)
			}
		case errors.Is(err, ErrNoOpenSet):
			if uErr := beat.Disposition(de.Inbox, path, "unmatched"); uErr != nil {
				return fmt.Errorf("decline: unmatch %s: %w", path, uErr)
			}
		default:
			if qErr := beat.Disposition(de.Inbox, path, "quarantine"); qErr != nil {
				return fmt.Errorf("decline: quarantine %s: %w", path, qErr)
			}
		}
		return nil
	})
}
