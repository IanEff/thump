package thump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/contract"
	"sigs.k8s.io/yaml"
)

type Transport struct {
	Inbox   string
	Outbox  string
	Catalog *contract.StaticCatalog
	Log     *OutcomeLog
	Exec    Executor
	Now     func() time.Time
}

// Tick performs one poll pass: list inbox → unmarshal Governed → render →
// execute → write orders/<name>.yaml + outcomes/<name>.yaml → archive to
// processed/. Only inbox-level I/O failures return an error; a bad envelope
// is a disposition (quarantine/skipped), never a crash.
func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	matches, err := filepath.Glob(filepath.Join(tr.Inbox, "*.yaml"))
	if err != nil {
		return fmt.Errorf("thump: list inbox: %w", err)
	}

	now := time.Now
	if tr.Now != nil {
		now = tr.Now
	}

	var act Actuator
	for _, path := range matches {
		raw, err := os.ReadFile(path) //nolint:gosec // G304: inbox path is operator config, not user input
		if err != nil {
			return fmt.Errorf("thump: read %s: %w", path, err)
		}

		var g decision.Governed
		if err := yaml.Unmarshal(raw, &g); err != nil {
			if qErr := tr.disposition(path, "quarantine"); qErr != nil {
				return fmt.Errorf("thump: quarantine %s: %w", path, qErr)
			}
			continue // poison doesn't block the queue
		}

		if g.Decision.Verdict != decision.VerdictApproved {
			if sErr := tr.disposition(path, "skipped"); sErr != nil {
				return fmt.Errorf("thump: skip %s: %w", path, sErr)
			}
			continue // a valid non-approval, just not ours to act on
		}

		order, err := act.Render(g, tr.Catalog, now())
		if err != nil {
			// a governed approval thump can't render is evidence of a seam
			// bug — same instinct as poison: keep it where a human will look.
			if qErr := tr.disposition(path, "quarantine"); qErr != nil {
				return fmt.Errorf("thump: quarantine %s: %w", path, qErr)
			}
			continue
		}
		outcome := tr.Exec.Execute(ctx, order, now())

		if err := tr.writeYAML(filepath.Join(tr.Outbox, "orders"), path, order); err != nil {
			return fmt.Errorf("thump: write order for %s: %w", path, err)
		}
		if err := tr.writeYAML(filepath.Join(tr.Outbox, "outcomes"), path, outcome); err != nil {
			return fmt.Errorf("thump: write outcome for %s: %w", path, err)
		}
		tr.Log.Record(outcome)

		if err := tr.disposition(path, "processed"); err != nil {
			return fmt.Errorf("thump: archive %s: %w", path, err)
		}
	}
	return nil
}

func (tr *Transport) writeYAML(dir, srcPath string, v any) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return writeAtomic(dir, filepath.Base(srcPath), out)
}

func (tr *Transport) disposition(path, sub string) error {
	dir := filepath.Join(tr.Inbox, sub)
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
