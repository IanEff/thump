package thump

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/otelx"
	"github.com/ianeff/thump/internal/publish"
)

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
	HeldPub    publish.Publisher[decision.Governed] // destination for holds — thump.held in production; never declined, so the fingerprint stays open
	Catalog    *contract.StaticCatalog              // the authored actions Render may resolve a granted Candidate against
	Log        *OutcomeLog                          // every Outcome produced, queryable by ByResult
	Exec       Executor                             // how an Order is carried out — DryRun in v1
	Reversal   *ReversalWatcher                     // fires the authred undo when a live forward Order's success window elapses unmet.
	Acceptance *AcceptanceWatcher                   // polls a release-mode order's acceptance before any convergence watch can mean anything; nil in production until a real ReleaseProbe exists, same as Reversal when unconfigured.
	Now        func() time.Time                     // overridable clock for deterministic tests; nil means time.Now
	Tracer     trace.Tracer                         // spans "render" under whatever trace ctx already carries; nil-safe via tracer()
	Stages     *beat.StageRecorder                  // RED metrics for "render" — nil-safe, same discipline as Tracer
	Notifier   Notifier                             // delivers a held action to a human; nil means a hold publishes to HeldPub only
}

// Tick performs one poll pass: list inbox → unmarshal Governed → handle
// (render → execute → publish) → disposition. Only inbox-level I/O failures
// return an error; a bad envelope or an unrenderable approval is a
// disposition (quarantine/skipped), never a crash.
func (tr *Transport) Tick(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return beat.DrainDir(tr.Inbox, "thump", func(path string, g decision.Governed) error {
		if err := tr.handle(ctx, g, nil); err != nil {
			if errors.Is(err, ErrRenderFailed) {
				// a governed approval thump can't render is evidence of a seam
				// bug — same instinct as poison: keep it where a human will look.
				if qErr := beat.Disposition(tr.Inbox, path, "quarantine"); qErr != nil {
					return fmt.Errorf("thump: quarantine %s: %w", path, qErr)
				}
				return nil
			}
			return fmt.Errorf("thump: handle %s: %w", path, err) // a publish failure aborts the pass
		}

		disp := "processed"
		if g.Decision.Verdict != decision.VerdictApproved {
			disp = "skipped" // a valid non-approval, just not ours to act on
		}
		if err := beat.Disposition(tr.Inbox, path, disp); err != nil {
			return fmt.Errorf("thump: archive %s: %w", path, err)
		}
		return nil
	})
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
		if err := beat.Stage(ctx, otelx.TracerOrNoop(tr.Tracer), tr.Stages, "render", func(context.Context) error {
			var err error
			order, err = (Actuator{}).Render(g, tr.Catalog, tr.now())
			return err
		}); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrRenderFailed, g.Decision.SignalRef, err)
		}

		var oc outcome.Outcome
		_ = beat.Stage(ctx, otelx.TracerOrNoop(tr.Tracer), tr.Stages, "execute", func(sctx context.Context) error {
			oc = tr.Exec.Execute(sctx, order, tr.now())
			if oc.Result == outcome.ResultFailure {
				if oc.Error != "" {
					return errors.New(oc.Error)
				}
				return errors.New("execute failed")
			}
			return nil
		})
		switch {
		case tr.Reversal != nil && oc.Mode == outcome.ModeLive && oc.Result == outcome.ResultApplied:
			// a cluster mutation ran directly — watch its SLO convergence window.
			go tr.watchAndSettle(ctx, order)
		case tr.Acceptance != nil && oc.Mode == outcome.ModeLive && oc.Result == outcome.ResultProposed:
			// a release-mode order: nothing in the cluster moved yet, only a
			// reviewable artifact exists — poll whether a human ever merged it
			// before any convergence watch can mean anything.
			go tr.watchAndAccept(ctx, order)
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
		if err := tr.HeldPub.Publish(ctx, "thump.held", g); err != nil {
			return fmt.Errorf("thump: publish held for %s: %w", g.Decision.SignalRef, err)
		}
		notified := tr.notify(ctx, g)
		slog.Info("held", "signalRef", g.Decision.SignalRef,
			"contractRef", g.Set.ContractRefFor(g.Decision.CandidateRef),
			"riskBand", g.Decision.RiskBand, "reasons", g.Decision.Reasons,
			"acted", false, "notified", notified)
		return nil
	default: // escalate, rejected — free the lock
		if err := tr.DeclinePub.Publish(ctx, "thump.declines", g.Decision); err != nil {
			return fmt.Errorf("thump: publish decline for %s: %w", g.Decision.SignalRef, err)
		}
		notified := tr.notify(ctx, g)
		slog.Info("outcome", "signalRef", g.Decision.SignalRef, "verdict", g.Decision.Verdict, "reasons", g.Decision.Reasons,
			"contractRef", g.Set.ContractRefFor(g.Decision.CandidateRef), "acted", false, "notified", notified)
		return nil // valid non-approval: nothing to act on
	}
}

// notify delivers g to Notifier when its verdict still needs a human ack —
// AwaitsApproval is the single source of that rule, so hold and escalate can
// never drift apart again. Best-effort: an error is logged, never returned,
// same contract the hold path always had.
func (tr *Transport) notify(ctx context.Context, g decision.Governed) bool {
	if !g.Decision.Verdict.AwaitsApproval() || tr.Notifier == nil {
		return false
	}
	if err := tr.Notifier.Notify(ctx, g); err != nil {
		slog.Error("notify", "signalRef", g.Decision.SignalRef, "verdict", g.Decision.Verdict, "err", err)
		return false
	}
	return true
}

// now returns tr.Now() when set, or time.Now — the one clock handle and the
// watchAndSettle goroutine it spawns both read, so a frozen test clock
// covers both.
func (tr *Transport) now() time.Time {
	return beat.Clock(tr.Now)()
}

// watchAndSettle blocks for order's success window, reads convergence once,
// and emits the terminal convergence Outcome — success when the SLO
// recovered, partial_non_converging when it did not. It then fires Undo when
// the Settlement says to: on non-convergence always, on a win only when the
// contract authored a restore — because either way the undo was part of the
// grant hiss already approved. One window, one probe read, two effects. Runs
// in its own goroutine so handle returns immediately; ctx is the same
// long-lived ctx the poll loop or consumer runs under, cancelled only at
// shutdown.
func (tr *Transport) watchAndSettle(ctx context.Context, order Order) {
	var settlement Settlement
	_ = beat.Stage(ctx, otelx.TracerOrNoop(tr.Tracer), tr.Stages, "settle", func(sctx context.Context) error {
		settlement = tr.Reversal.Watch(sctx, order)
		if sctx.Err() != nil {
			return sctx.Err()
		}
		if !settlement.Converged {
			return errors.New("non-converging")
		}
		return nil
	})
	if ctx.Err() != nil {
		return // shutdown mid-watch, not a real convergence read — nothing to settle
	}

	conv := outcome.Outcome{
		ID:               fmt.Sprintf("out:%s:conv:%d", order.SignalRef, tr.now().Unix()),
		DecisionRef:      order.DecisionRef, // answers to the same grant → the same ledger set
		SignalRef:        order.SignalRef,
		ContractRef:      order.ContractRef,
		Mode:             outcome.ModeLive,
		Result:           settleResult(settlement.Converged),
		ObservedSeverity: settlement.Severity, // nil stays nil — unmeasured never becomes a fabricated 0.0
		Error:            settleError(settlement.Converged, settlement.Held, order.Success.Target, order.Reversal.Fallback),
		ExecutedAt:       tr.now(),
	}
	if err := tr.OutcomePub.Publish(ctx, "thump.outcomes", conv); err != nil {
		slog.Error("publish convergence outcome", "signalRef", order.SignalRef, "err", err)
	}
	tr.Log.Record(conv)
	slog.Info("settled", "signalRef", order.SignalRef, "contractRef", order.ContractRef,
		"result", conv.Result, "observedSeverity", logSeverity(settlement.Severity), "fired", settlement.Fire,
		"held", settlement.Held)

	if !settlement.Fire {
		return
	}
	undo := settlement.Undo
	var oc outcome.Outcome
	_ = beat.Stage(ctx, otelx.TracerOrNoop(tr.Tracer), tr.Stages, "undo", func(sctx context.Context) error {
		oc = tr.Exec.Execute(sctx, undo, tr.now())
		if oc.Result == outcome.ResultFailure {
			if oc.Error != "" {
				return errors.New(oc.Error)
			}
			return errors.New("undo failed")
		}
		return nil
	})
	if err := tr.OrderPub.Publish(ctx, "thump.orders", undo); err != nil {
		slog.Error("publish undo order", "signalRef", undo.SignalRef, "err", err)
	}
	if err := tr.OutcomePub.Publish(ctx, "thump.outcomes", oc); err != nil {
		slog.Error("publish undo outcome", "signalRef", undo.SignalRef, "err", err)
	}
	tr.Log.Record(oc)
	slog.Info("outcome", "signalRef", undo.SignalRef, "contractRef", oc.ContractRef, "acted", true, "reversal", true)
}

// watchAndAccept polls order's release-mode acceptance once its success
// window elapses. Not accepted means nobody merged the release — nothing
// applied, so the terminal record is partial_non_converging carrying the
// authored fallback, and no convergence watch starts. Accepted means Argo
// already applied it — from here on it's an ordinary live order, so this
// falls straight into watchAndSettle unchanged. A process restart loses an
// in-flight poll the same way it already loses an in-flight watchAndSettle
// (the bare go above) — accepted parity, not new debt.
func (tr *Transport) watchAndAccept(ctx context.Context, order Order) {
	var accepted bool
	var pollErr error
	_ = beat.Stage(ctx, otelx.TracerOrNoop(tr.Tracer), tr.Stages, "acceptance_poll", func(sctx context.Context) error {
		accepted, pollErr = tr.Acceptance.Poll(sctx, order)
		if sctx.Err() != nil {
			return sctx.Err()
		}
		if pollErr != nil {
			return pollErr
		}
		if !accepted {
			return errors.New("release not accepted")
		}
		return nil
	})
	if ctx.Err() != nil {
		return // shutdown mid-poll, not a real acceptance read — nothing to settle
	}
	if pollErr != nil {
		tr.recordAccept(ctx, order, outcome.ResultFailure, fmt.Sprintf("acceptance poll failed: %v", pollErr))
		return
	}
	if !accepted {
		tr.recordAccept(ctx, order, outcome.ResultPartialNonConverging, acceptanceError(order.Reversal.Fallback))
		return
	}
	tr.watchAndSettle(ctx, order)
}

// recordAccept publishes and logs the terminal Outcome of an acceptance
// poll — not-accepted or a probe error both end the release's story right
// here, with no convergence watch to follow.
func (tr *Transport) recordAccept(ctx context.Context, order Order, result outcome.Result, errText string) {
	oc := outcome.Outcome{
		ID:          fmt.Sprintf("out:%s:accept:%d", order.SignalRef, tr.now().Unix()),
		DecisionRef: order.DecisionRef,
		SignalRef:   order.SignalRef,
		ContractRef: order.ContractRef,
		Mode:        outcome.ModeLive,
		Result:      result,
		Error:       errText,
		ExecutedAt:  tr.now(),
	}
	if err := tr.OutcomePub.Publish(ctx, "thump.outcomes", oc); err != nil {
		slog.Error("publish acceptance outcome", "signalRef", order.SignalRef, "err", err)
	}
	tr.Log.Record(oc)
	slog.Info("acceptance", "signalRef", order.SignalRef, "contractRef", oc.ContractRef, "result", oc.Result)
}

// acceptanceError renders the authored fallback for a release the window
// closed on with nobody merging — the fallback is the operator-facing
// reason, not a generic message.
func acceptanceError(fallback string) string {
	if fallback == "" {
		return "release not accepted within the success window; no fallback authored"
	}
	return fmt.Sprintf("release not accepted within the success window; fallback: %s", fallback)
}

// logSeverity renders a nil severity as "unmeasured" for the slog line rather than
// a pointer address or a misleading 0 — the log has to be as honest as the field.
func logSeverity(s *float64) any {
	if s == nil {
		return "unmeasured"
	}
	return *s
}

// settleResult maps the Settlement's convergence verdict to the terminal
// result — Fire plays no part, so a restored win still settles as success.
func settleResult(converged bool) outcome.Result {
	if converged {
		return outcome.ResultSuccess
	}
	return outcome.ResultPartialNonConverging
}

// settleError gives a partial the error text Auditable() demands — silence on a
// non-convergence is exactly the accountability gap I-6 defence 4 exists to close.
// A held miss names its escalation the same way acceptanceError does, since
// HoldOnMiss suppressed the undo an ordinary contract would have fired.
func settleError(converged, held bool, target, fallback string) string {
	if converged {
		return ""
	}
	if held {
		if fallback == "" {
			return fmt.Sprintf("success window elapsed without meeting %q; undo held, no fallback authored", target)
		}
		return fmt.Sprintf("success window elapsed without meeting %q; undo held, fallback: %s", target, fallback)
	}
	return fmt.Sprintf("success window elapsed without meeting %q", target)
}
