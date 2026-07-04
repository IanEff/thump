package clank

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ianeff/thump/internal/signal"
	"sigs.k8s.io/yaml"
)

type Transport struct {
	Inbox    string
	Engine   *Engine
	attempts map[string]int
}

const maxProposeAttempts = 5

func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	matches, err := filepath.Glob(filepath.Join(tr.Inbox, "*.yaml"))
	if err != nil {
		return fmt.Errorf("clank: list inbox: %w", err)
	}
	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return fmt.Errorf("clank: read %s: %w", path, err)
		}
		var det signal.Detection
		if err := yaml.Unmarshal(raw, &det); err != nil {
			if qErr := tr.disposition(path, "quarantine"); qErr != nil {
				return fmt.Errorf("clank: quarantine %s: %w", path, qErr)
			}
			continue // poison doesn't block the queue — the hiss/thump/click rule
		}
		set, err := tr.Engine.Propose(ctx, det)
		if err != nil {
			if tr.attempts == nil {
				tr.attempts = make(map[string]int)
			}
			tr.attempts[path]++
			if tr.attempts[path] >= maxProposeAttempts {
				slog.Error("giving up on detection", "path", path, "attempts", tr.attempts[path], "err", err)
				delete(tr.attempts, path)
				if dErr := tr.disposition(path, "stalled"); dErr != nil {
					return fmt.Errorf("clank: stall %s: %w", path, dErr)
				}
				continue
			}
			slog.Warn("propose failed, will retry", "path", path, "attempts", tr.attempts[path], "err", err)
			continue
		}
		delete(tr.attempts, path)

		slog.Info("reasoned", "fingerprint", det.Fingerprint, "phase", set.Status.Phase, "proposals", len(set.Proposals))
		if err := tr.disposition(path, "processed"); err != nil {
			return fmt.Errorf("clank: archive %s: %w", path, err)
		}
	}
	return nil
}

func (tr *Transport) disposition(path, sub string) error {
	dir := filepath.Join(tr.Inbox, sub)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}
