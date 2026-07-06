package hiss

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"sigs.k8s.io/yaml"
)

type Transport struct {
	Inbox  string
	Outbox string
	Policy Policy
	Log    *DecisionLog
	Now    func() time.Time
}

func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	matches, err := filepath.Glob(filepath.Join(tr.Inbox, "*.yaml"))
	if err != nil {
		return fmt.Errorf("hiss: list inbox: %w", err)
	}

	var auth Authority
	now := time.Now
	if tr.Now != nil {
		now = tr.Now
	}

	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return fmt.Errorf("hiss: read %s: %w", path, err)
		}

		var ps proposal.Set
		if err := yaml.Unmarshal(raw, &ps); err != nil {
			if qErr := tr.quarantine(path); qErr != nil {
				return fmt.Errorf("hiss: quarantine %s: %w", path, err)
			}
			continue // poison doesn't block the queue
		}

		d := auth.Evaluate(ps, tr.Policy, now())
		tr.Log.Record(d)

		out, err := yaml.Marshal(decision.Governed{Decision: d, Set: ps})
		if err != nil {
			return fmt.Errorf("hiss: marshal decision for %s: %w", path, err)
		}

		if err := writeAtomic(tr.Outbox, filepath.Base(path), out); err != nil {
			return fmt.Errorf("hiss: write decision for %s: %w", path, err)
		}
		if err := tr.archive(path); err != nil {
			return fmt.Errorf("hiss: archive %s: %w", path, err)
		}
	}
	return nil
}

func (tr *Transport) quarantine(path string) error {
	dir := filepath.Join(tr.Inbox, "quarantine")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}

func (tr *Transport) archive(path string) error {
	dir := filepath.Join(tr.Inbox, "processed")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}

// writeAtomic is the simple atomic writer, replicated across all services to PROVE A POINT.
func writeAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*") // dot-prefixed, no .yaml suffix
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
