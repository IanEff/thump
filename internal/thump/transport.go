package thump

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
)

// HeldAction is a gate-clean Candidate whose computed risk exceeded
// Policy.AutoBand — eligible, but waiting on a human ack rather than
// auto-firing. Its fingerprint stays open (never declined, never executed),
// so the dedupe ledger keeps suppressing a fresh proposal until the window
// elapses. Carries the full audit chain (Decision + Set) rather than a
// rendered summary — a Notifier adapter derives human-facing text from it.
type HeldAction struct {
	Decision decision.Decision `json:"decision,omitempty" yaml:"decision,omitempty"`
	Set      proposal.Set      `json:"set,omitempty" yaml:"set,omitempty"`
}

// ErrRenderFailed marks a governed approval thump's Actuator couldn't render
// — a deterministic seam bug (bad catalog ref, bad params), not a transient
// failure. Tick quarantines it, same instinct as poison; it's what lets Tick
// tell "render failed" apart from "publish failed" after both collapse
// through handle's single error return.
var ErrRenderFailed = errors.New("thump: render failed")

// Transport is thump's directory-poll seam: it watches Inbox for
// decision.Governed files, renders and dry-run-executes each approval, and
// publishes the resulting Order and outcome.Outcome. It's the keyless fake
// the seam tests drive without a broker; thump.go's Main runs the NATS
// branch instead in production.
type Transport struct {
	Inbox      string                               // directory globbed for *.yaml decision.Governed files
	OrderPub   publish.Publisher[Order]             // destination for rendered Orders — thump.orders in production
	OutcomePub publish.Publisher[outcome.Outcome]   // destination for executed Outcomes — thump.outcomes in production
	DeclinePub publish.Publisher[decision.Decision] // destination for non-approvals — thump.declines in production; closes clank's ledger row without ever going through Outcome
	HeldPub    publish.Publisher[HeldAction]        // destination for holds — thump.held in production; never declined, so the fingerprint stays open
	Catalog    *contract.StaticCatalog              // the authored actions Render may resolve a granted Candidate against
	Log        *OutcomeLog                          // every Outcome produced, queryable by ByResult
	Exec       Executor                             // how an Order is carried out — DryRun in v1
	Reversal   *ReversalWatcher                     // fires the authred undo when a live forward Order's success window elapses unmet.
	Now        func() time.Time                     // overridable clock for deterministic tests; nil means time.Now
	Tracer     trace.Tracer                         // spans "render" under whatever trace ctx already carries; nil-safe via tracer()
	Stages     *beat.StageRecorder                  // RED metrics for "render" — nil-safe, same discipline as Tracer
	Notifier   Notifier                             // delivers a held action to a human; nil means a hold publishes to HeldPub only
}

// tracer returns Tracer, or a no-op if unset — handle never has to nil-check,
// and every existing test keeps compiling untouched. handle never mints a
// root or forces a TraceID: in production that context already arrived on
// ctx, propagated from hiss's publish over JetStream headers.
func (tr *Transport) tracer() trace.Tracer {
	if tr.Tracer == nil {
		return noop.Tracer{}
	}
	return tr.Tracer
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

		if err := tr.handle(ctx, g, nil); err != nil {
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
// Rendering a dry-run is fast enough that it never needs heartbeat, unlike
// clank's reason loop — accepted only to satisfy broker.Handler[T]'s shape.
func (tr *Transport) handle(ctx context.Context, g decision.Governed, _ func()) error {
	switch g.Decision.Verdict {
	case decision.VerdictApproved:
		var order Order
		if err := beat.Stage(ctx, tr.tracer(), tr.Stages, "render", func(context.Context) error {
			var err error
			order, err = (Actuator{}).Render(g, tr.Catalog, tr.now())
			return err
		}); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrRenderFailed, g.Decision.SignalRef, err)
		}
		oc := tr.Exec.Execute(ctx, order, tr.now())
		if tr.Reversal != nil && oc.Mode == outcome.ModeLive && oc.Result == outcome.ResultApplied {
			go tr.watchAndSettle(ctx, order)
		}
		if err := tr.OrderPub.Publish(ctx, "thump.orders", order); err != nil {
			return fmt.Errorf("thump: publish order for %s: %w", g.Decision.SignalRef, err)
		}
		if err := tr.OutcomePub.Publish(ctx, "thump.outcomes", oc); err != nil {
			return fmt.Errorf("thump: publish outcome for %s: %w", g.Decision.SignalRef, err)
		}
		tr.Log.Record(oc)
		slog.Info("outcome", "signalRef", g.Decision.SignalRef, "candidateRef", g.Decision.CandidateRef,
			"contractRef", oc.ContractRef, "acted", true, "mode", oc.Mode, "result", oc.Result, "error", oc.Error)
		return nil
	case decision.VerdictHold:
		held := HeldAction{Decision: g.Decision, Set: g.Set}
		if err := tr.HeldPub.Publish(ctx, "thump.held", held); err != nil {
			return fmt.Errorf("thump: publish held for %s: %w", g.Decision.SignalRef, err)
		}
		notified := false
		if tr.Notifier != nil {
			if err := tr.Notifier.Notify(ctx, held); err != nil {
				slog.Error("notify held action", "signalRef", g.Decision.SignalRef, "err", err)
			} else {
				notified = true
			}
		}
		slog.Info("held", "signalRef", g.Decision.SignalRef,
			"contractRef", g.Set.ContractRefFor(g.Decision.CandidateRef),
			"riskBand", g.Decision.RiskBand, "reasons", g.Decision.Reasons,
			"acted", false, "notified", notified)
		return nil
	default: // escalate, rejected — free the lock
		slog.Info("outcome", "signalRef", g.Decision.SignalRef, "verdict", g.Decision.Verdict, "reasons", g.Decision.Reasons,
			"contractRef", g.Set.ContractRefFor(g.Decision.CandidateRef), "acted", false)
		if err := tr.DeclinePub.Publish(ctx, "thump.declines", g.Decision); err != nil {
			return fmt.Errorf("thump: publish decline for %s: %w", g.Decision.SignalRef, err)
		}
		return nil // valid non-approval: nothing to act on
	}
}

// now returns tr.Now() when set, or time.Now — the one clock handle and the
// watchAndSettle goroutine it spawns both read, so a frozen test clock
// covers both.
func (tr *Transport) now() time.Time {
	if tr.Now != nil {
		return tr.Now()
	}
	return time.Now()
}

func (tr *Transport) disposition(path, sub string) error {
	dir := filepath.Join(tr.Inbox, sub)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}

// watchAndSettle blocks for order's success window, reads convergence once,
// and emits the terminal convergence Outcome — success when the SLO
// recovered, partial_non_converging when it did not. On non-convergence it
// also fires the authored reversal (unchanged from the old watchAndReverse),
// because the undo was part of the grant hiss already approved. One window,
// one probe read, two effects. Runs in its own goroutine so handle returns
// immediately; ctx is the same long-lived ctx the poll loop or consumer runs
// under, cancelled only at shutdown.
func (tr *Transport) watchAndSettle(ctx context.Context, order Order) {
	reversal, fired, severity := tr.Reversal.Watch(ctx, order)

	conv := outcome.Outcome{
		ID:               fmt.Sprintf("out:%s:conv:%d", order.SignalRef, tr.now().Unix()),
		DecisionRef:      order.DecisionRef, // answers to the same grant → the same ledger set
		SignalRef:        order.SignalRef,
		ContractRef:      order.ContractRef,
		Mode:             outcome.ModeLive,
		Result:           settleResult(fired),
		ObservedSeverity: severity, // nil stays nil — unmeasured never becomes a fabricated 0.0
		Error:            settleError(fired, order.Success.Target),
		ExecutedAt:       tr.now(),
	}
	if err := tr.OutcomePub.Publish(ctx, "thump.outcomes", conv); err != nil {
		slog.Error("publish convergence outcome", "signalRef", order.SignalRef, "err", err)
	}
	tr.Log.Record(conv)
	slog.Info("settled", "signalRef", order.SignalRef, "contractRef", order.ContractRef,
		"result", conv.Result, "observedSeverity", logSeverity(severity), "reversed", fired)

	if !fired {
		return
	}
	oc := tr.Exec.Execute(ctx, reversal, tr.now())
	if err := tr.OrderPub.Publish(ctx, "thump.orders", reversal); err != nil {
		slog.Error("publish reversal order", "signalRef", reversal.SignalRef, "err", err)
	}
	if err := tr.OutcomePub.Publish(ctx, "thump.outcomes", oc); err != nil {
		slog.Error("publish reversal outcome", "signalRef", reversal.SignalRef, "err", err)
	}
	tr.Log.Record(oc)
	slog.Info("outcome", "signalRef", reversal.SignalRef, "contractRef", oc.ContractRef, "acted", true, "reversal", true)
}

// logSeverity renders a nil severity as "unmeasured" for the slog line rather than
// a pointer address or a misleading 0 — the log has to be as honest as the field.
func logSeverity(s *float64) any {
	if s == nil {
		return "unmeasured"
	}
	return *s
}

// settleResult maps the watcher's did-it-reverse bool to the terminal result:
// a fired reversal means the window elapsed unmet.
func settleResult(fired bool) outcome.Result {
	if fired {
		return outcome.ResultPartialNonConverging
	}
	return outcome.ResultSuccess
}

// settleError gives a partial the error text Auditable() demands — silence on a
// non-convergence is exactly the accountability gap I-6 defence 4 exists to close.
func settleError(fired bool, target string) string {
	if fired {
		return fmt.Sprintf("success window elapsed without meeting %q", target)
	}
	return ""
}
