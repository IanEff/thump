package thump

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
)

// ErrRenderFailed marks a governed approval thump's Actuator couldn't render
// — a deterministic seam bug (bad catalog ref, bad params), not a transient
// failure. Tick quarantines it, same instinct as poison; it's what lets Tick
// tell "render failed" apart from "publish failed" after both collapse
// through handle's single error return.
var ErrRenderFailed = errors.New("thump: render failed")

type Transport struct {
	Inbox      string
	OrderPub   publish.Publisher[Order]
	OutcomePub publish.Publisher[outcome.Outcome]
	Catalog    *contract.StaticCatalog
	Log        *OutcomeLog
	Exec       Executor
	Now        func() time.Time
}

// Tick performs one poll pass: list inbox → unmarshal Governed → handle
// (render → execute → publish) → disposition. Only inbox-level I/O failures
// return an error; a bad envelope or an unrenderable approval is a
// disposition (quarantine/skipped), never a crash.
func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	matches, err := filepath.Glob(filepath.Join(tr.Inbox, "*.yaml"))
	if err != nil {
		return fmt.Errorf("thump: list inbox: %w", err)
	}

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

		if err := tr.handle(ctx, g); err != nil {
			if errors.Is(err, ErrRenderFailed) {
				// a governed approval thump can't render is evidence of a seam
				// bug — same instinct as poison: keep it where a human will look.
				if qErr := tr.disposition(path, "quarantine"); qErr != nil {
					return fmt.Errorf("thump: quarantine %s: %w", path, qErr)
				}
				continue
			}
			return fmt.Errorf("thump: handle %s: %w", path, err) // a publish failure aborts the pass
		}

		disp := "processed"
		if g.Decision.Verdict != decision.VerdictApproved {
			disp = "skipped" // a valid non-approval, just not ours to act on
		}
		if err := tr.disposition(path, disp); err != nil {
			return fmt.Errorf("thump: archive %s: %w", path, err)
		}
	}
	return nil
}

// handle renders, dry-run-executes, and publishes one governed approval —
// the transport-independent core. Tick calls it after decoding a file; the
// NATS handler calls it after decoding a message. Same brain, two feeders.
func (tr *Transport) handle(ctx context.Context, g decision.Governed) error {
	if g.Decision.Verdict != decision.VerdictApproved {
		slog.Info("outcome", "signalRef", g.Decision.SignalRef, "verdict", g.Decision.Verdict, "reasons", g.Decision.Reasons, "acted", false)
		return nil // valid non-approval: nothing to act on
	}
	now := time.Now
	if tr.Now != nil {
		now = tr.Now
	}
	order, err := (Actuator{}).Render(g, tr.Catalog, now())
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrRenderFailed, g.Decision.SignalRef, err)
	}
	oc := tr.Exec.Execute(ctx, order, now())
	if err := tr.OrderPub.Publish(ctx, "thump.orders", order); err != nil {
		return fmt.Errorf("thump: publish order for %s: %w", g.Decision.SignalRef, err)
	}
	if err := tr.OutcomePub.Publish(ctx, "thump.outcomes", oc); err != nil {
		return fmt.Errorf("thump: publish outcome for %s: %w", g.Decision.SignalRef, err)
	}
	tr.Log.Record(oc)
	slog.Info("outcome", "signalRef", g.Decision.SignalRef, "candidateRef", g.Decision.CandidateRef, "acted", true)
	return nil
}

func (tr *Transport) disposition(path, sub string) error {
	dir := filepath.Join(tr.Inbox, sub)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}
