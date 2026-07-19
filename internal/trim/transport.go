package trim

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"sigs.k8s.io/yaml"
)

type Transport struct {
	Inbox string      // root directory.
	Proj  *Projection // where Tick lands every successfully-folded object; unused by Snapshot, which builds and returns its own
}

func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := tickDir[signal.Detection](tr, "detections"); err != nil {
		return err
	}
	if err := tickDir[proposal.Set](tr, "proposals"); err != nil {
		return err
	}
	if err := tickDir[decision.Governed](tr, "decisions"); err != nil {
		return err
	}
	if err := tickDir[outcome.Outcome](tr, "outcomes"); err != nil {
		return err
	}
	return nil
}

func tickDir[T any](tr *Transport, dir string) error {
	root := filepath.Join(tr.Inbox, dir)
	matches, err := filepath.Glob(filepath.Join(root, "*.yaml"))
	if err != nil {
		return fmt.Errorf("trim: list %s: %w", root, err)
	}

	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return fmt.Errorf("trim: read %s: %w", path, err)
		}

		var v T
		if err := yaml.Unmarshal(raw, &v); err != nil {
			if qErr := quarantine(path); qErr != nil {
				return fmt.Errorf("trim: quarantine %s: %w", path, qErr)
			}
			continue
		}
		if err := tr.Proj.Apply(v); err != nil {
			if qErr := quarantine(path); qErr != nil {
				return fmt.Errorf("trim: quarantine %s: %w", path, qErr)
			}
			continue
		}
		if err := archive(path); err != nil {
			return fmt.Errorf("trim: archive %s: %w", path, err)
		}
	}
	return nil
}

// Snapshot builds a fresh Projection from every boundary object currently on
// disk under Inbox — both freshly arrived and already archived under
// processed/ — without moving or mutating anything. Tick archives what it
// processes, which is correct for a long-running consumer that keeps its
// Projection alive across many polls; a one-shot query has no such memory
// between invocations, so Snapshot has to find the full history itself and
// stays safe to call repeatedly.
func (tr *Transport) Snapshot(ctx context.Context) (*Projection, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	proj := NewProjection()
	if err := snapshotDir[signal.Detection](tr, "detections", proj); err != nil {
		return nil, err
	}
	if err := snapshotDir[proposal.Set](tr, "proposals", proj); err != nil {
		return nil, err
	}
	if err := snapshotDir[decision.Governed](tr, "decisions", proj); err != nil {
		return nil, err
	}
	if err := snapshotDir[outcome.Outcome](tr, "outcomes", proj); err != nil {
		return nil, err
	}
	return proj, nil
}

func snapshotDir[T any](tr *Transport, dir string, proj *Projection) error {
	root := filepath.Join(tr.Inbox, dir)
	patterns := []string{
		filepath.Join(root, "*.yaml"),
		filepath.Join(root, "processed", "*.yaml"),
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("trim: list %s: %w", pattern, err)
		}
		for _, path := range matches {
			raw, err := os.ReadFile(path) //nolint:gosec
			if err != nil {
				return fmt.Errorf("trim: read %s: %w", path, err)
			}
			var v T
			if err := yaml.Unmarshal(raw, &v); err != nil {
				continue // read-only — skip what Tick would have quarantined
			}
			if err := proj.Apply(v); err != nil {
				continue // same — no fingerprint, nothing to do but skip
			}
		}
	}
	return nil
}

func quarantine(path string) error {
	return relocate(path, "quarantine")
}

func archive(path string) error {
	return relocate(path, "processed")
}

func relocate(path, subdir string) error {
	dir := filepath.Join(filepath.Dir(path), subdir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}
