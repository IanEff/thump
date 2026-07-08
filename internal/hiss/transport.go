package hiss

import (
	"context"
	"fmt"
	"log/slog"
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

		if err := tr.handle(ctx, ps); err != nil {
			return fmt.Errorf("hiss: handle %s: %w", path, err)
		}
		if err := tr.archive(path); err != nil {
			return fmt.Errorf("hiss: archive %s: %w", path, err)
		}
	}
	return nil
}

// handle evaluates one ProposalSet and publishes the Governed decision — the
// transport-independent core. Tick calls it after decoding a file; the NATS
// handler calls it after decoding a message. Same brain, two feeders.
func (tr *Transport) handle(ctx context.Context, ps proposal.Set) error {
	now := time.Now
	if tr.Now != nil {
		now = tr.Now
	}
	var auth Authority
	d := auth.Evaluate(ps, tr.Policy, now())
	tr.Log.Record(d)
	slog.Info("decision", "fingerprint", ps.SignalRef, "verdict", d.Verdict, "reasons", d.Reasons, "requestedBand", d.RequestedBand, "grantedBand", d.GrantedBand)
	return tr.Pub.Publish(ctx, "thump.decisions", decision.Governed{Decision: d, Set: ps})
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
