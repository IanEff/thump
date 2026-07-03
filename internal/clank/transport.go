package clank

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ianeff/clank/internal/signal"
	"sigs.k8s.io/yaml"
)

type Transport struct {
	Inbox  string
	Engine *Engine
}

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
		if _, err := tr.Engine.Propose(ctx, det); err != nil {
			// a reasoning error (LLM down, tool failure) is retryable — leave the
			// file for the next tick, log, and move on. Do NOT quarantine a valid
			// detection just because the model hiccuped.
			return fmt.Errorf("clank: propose %s: %w", path, err)
		}
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
