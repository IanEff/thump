package clank

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ianeff/thump/api/v1/decision"
	"sigs.k8s.io/yaml"
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
	matches, err := filepath.Glob(filepath.Join(de.Inbox, "*.yaml"))
	if err != nil {
		return fmt.Errorf("decline: list inbox: %w", err)
	}

	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return fmt.Errorf("decline: read %s: %w", path, err)
		}

		var dec decision.Decision
		if err := yaml.Unmarshal(raw, &dec); err != nil {
			if qErr := de.disposition(path, "quarantine"); qErr != nil {
				return fmt.Errorf("decline: quarantine %s: %w", path, qErr)
			}
			continue
		}

		switch _, err := de.Ledger.Decline(ctx, dec.SignalRef, dec.EvaluatedAt); {
		case err == nil:
			if pErr := de.disposition(path, "processed"); pErr != nil {
				return fmt.Errorf("decline: archive %s: %w", path, pErr)
			}
		case errors.Is(err, ErrNoOpenSet):
			if uErr := de.disposition(path, "unmatched"); uErr != nil {
				return fmt.Errorf("decline: unmatch %s: %w", path, uErr)
			}
		default:
			if qErr := de.disposition(path, "quarantine"); qErr != nil {
				return fmt.Errorf("decline: quarantine %s: %w", path, qErr)
			}
		}
	}
	return nil
}

func (de *DeclineEdge) disposition(path, sub string) error {
	dir := filepath.Join(de.Inbox, sub)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}
