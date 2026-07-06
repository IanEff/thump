package hiss

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
)

type Transport struct {
	Inbox  string
	Pub    publish.Publisher[decision.Governed]
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

		if err := tr.Pub.Publish(ctx, "thump.decisions", decision.Governed{Decision: d, Set: ps}); err != nil {
			return fmt.Errorf("hiss: publish decision for %s: %w", path, err)
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
