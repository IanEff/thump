package hiss

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
)

// Transport is hiss's directory-poll seam: it watches Inbox for proposal.Set
// files, governs each one through Authority.Evaluate, and publishes the
// resulting decision.Governed envelope. It's the keyless fake the seam
// tests drive without a broker; hiss.go's Main runs the NATS branch instead
// in production.
type Transport struct {
	Inbox  string                               // directory globbed for *.yaml proposal.Set files
	Pub    publish.Publisher[decision.Governed] // destination for governed decisions — thump.decisions in production
	Policy Policy                               // the floors, ceilings, and freeze windows Authority.Evaluate governs against
	Log    *DecisionLog                         // every Decision reached, queryable by ByVerdict
	Now    func() time.Time                     // overridable clock for deterministic tests; nil means time.Now
	Tracer trace.Tracer                         // spans "govern" under whatever trace ctx already carries; nil-safe via tracer()
}

// tracer returns Tracer, or a no-op if unset — handle never has to nil-check,
// and every existing test keeps compiling untouched. handle never mints a
// root or forces a TraceID: in production that context already arrived on
// ctx, propagated from clank's publish over JetStream headers.
func (tr *Transport) tracer() trace.Tracer {
	if tr.Tracer == nil {
		return noop.Tracer{}
	}
	return tr.Tracer
}

// Tick performs one poll pass: list Inbox, decode each file, evaluate it
// through handle, and archive or quarantine the result. A file that fails to
// unmarshal is quarantined, not deleted, and does not block the rest of the
// pass — one poison proposal.Set can't stall every other one behind it.
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
	_, span := tr.tracer().Start(ctx, "govern")
	var auth Authority
	d := auth.Evaluate(ps, tr.Policy, now())
	span.End()
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
