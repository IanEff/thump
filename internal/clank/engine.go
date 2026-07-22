package clank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
)

// errClassMismatch marks a proposed ContractRef that IS a real catalogued
// action, just not applicable to the FailureClass the model itself
// declared — a plausible-but-mislabelled proposal, not an invented one.
// Propose turns this into an auditable no_action decline; only a
// ContractRef naming no catalogued action at all (contract.ErrOutsideCatalog)
// halts the run.
var errClassMismatch = errors.New("candidate not applicable to declared failure class")

// errUngroundedCitation marks a proposed candidate citing evidence the run
// never gathered, or citing nothing at all — a causal claim with no
// inspectable basis. It declines auditably rather than halting: the model
// made a checkable mistake, not an out-of-catalog escape.
var errUngroundedCitation = errors.New("candidate cites evidence the run did not gather")

// Engine runs the bounded reason loop — one signal.Detection in, one
// proposal.Set out. It owns every seam the loop composes: the LLM, the
// read-only tools, the action catalog, the ranker, the readiness gate, the
// checkpoint Store, and the ledger. Nothing here reaches infrastructure;
// Propose only ever reads evidence and writes to the Store, the Ledger, and
// Pub.
type Engine struct {
	Intake         *Intake                           // assembles the versioned SAO the loop reasons over
	Model          Model                             // the LLM seam, faked in tests
	Tools          map[string]Tool                   // read-only evidence tools, keyed by the name the model calls
	Catalog        *contract.StaticCatalog           // the autonomy boundary: enforceCatalog rejects any proposed ContractRef this doesn't list
	FailureClasses []contract.FailureClassDefinition // authored, rig-invariant meaning of each class, rendered into seedPrompt; nil renders no block, so a bare-bones test Engine still works
	Ranker         *Ranker                           // orders the formed candidates once, after the loop exits
	Gate           ReadinessGate                     // budget ∧ dedup ∧ evidence, evaluated once on the formed set
	Store          Store                             // loop memory: one checkpoint per turn, a different lifetime from Ledger
	Scorer         CausalScorer                      // rates each change event's likelihood of causing the signal
	Prior          Prior                             // scoreConfidence's corroboration read — the same case base CausalScorerImpl.Prior points at; Engine needs its own reference because CausalScorer never exposes the one it holds
	DedupeWindow   time.Duration                     // how far back Ledger.Open looks for a live set on the same fingerprint
	Ledger         *MemProposalLog                   // every Propose run is recorded here, gated or not — the audit trail
	Pub            publish.Publisher[proposal.Set]   // delivery — only called when the gate passes
	MaxSteps       int                               // hard bound on reason-loop turns; exhausting it without a propose/insufficient call ends the run budget-exhausted
	Weights        ScoringWeights
	Tracer         trace.Tracer        // spans the reason-loop stages under whatever trace ctx already carries; nil-safe via tracer() so existing callers need not set it
	Stages         *beat.StageRecorder // RED metrics per stage — nil-safe, same discipline as Tracer; every Propose call still logs and spans without one
}

// tracer returns Tracer, or a no-op if unset — Propose never has to nil-check,
// and every test that doesn't care about tracing keeps compiling untouched.
// Propose never mints a root or forces a TraceID itself: in production that
// context already arrived on ctx (rattle mints it from the Fingerprint and
// propagates it over JetStream headers before clank's transport ever calls
// Propose), so every span here is an ordinary child of whatever ctx it's given.
func (e *Engine) tracer() trace.Tracer {
	if e.Tracer == nil {
		return noop.Tracer{}
	}
	return e.Tracer
}

// Propose turns one signal.Detection into a proposal.Set. It assembles the
// SAO via Intake, then drives the model for at most MaxSteps turns: each turn
// dispatches the model's tool calls (a read-only evidence tool loops back a
// one-line digest, never raw data; "propose" or "insufficient" ends the run)
// and checkpoints the turn to Store before the next one runs — a checkpoint
// error halts the run rather than risk an unrecorded turn, and re-running is
// always safe because nothing in the loop mutates infrastructure.
//
// A run that exhausts MaxSteps without ever calling propose or insufficient
// is recorded as budget-exhausted, not returned as an error. Every candidate
// action the model does propose must resolve to a ContractRef the Catalog
// lists for this signal's failure class, tier, and SAO — an out-of-catalog
// ref fails the run outright; the autonomy boundary is enforced here, not
// hoped for.
//
// Once the loop exits with a proposal, ranking and the gate run exactly once
// on the formed set: the Ranker orders candidates velocity-weighted off the
// signal's blast radius, and the Gate — a conjunction of budget, dedup, and
// evidence minimums, never an average — decides whether the set is worth
// emitting. The set is Recorded to the Ledger either way; it is only
// published through Pub when the gate passes, and an open set for the same
// fingerprint suppresses (but still records) a new one.
func (e *Engine) Propose(ctx context.Context, sig signal.Detection) (set proposal.Set, err error) {
	// runID, not sig.Fingerprint alone, keys every Store call below — two
	// invocations for the same fingerprint (a legitimate re-fire after
	// rattle's debounce window, a JetStream redelivery, a retry after a
	// transient error) must never share checkpoint objects, or the second
	// run silently clobbers the first's transcript at each matching step.
	runID := fmt.Sprintf("%s/%d", sig.Fingerprint, time.Now().UnixNano())
	defer func() { _ = e.Store.Finish(ctx, runID, err) }()

	// step is hoisted out of the reason loop below (`for ; step < ...`
	// instead of `for step := 0; ...`) so the terminal log deferred next
	// can report how far the run got on every exit path, not just the
	// ones lexically inside the loop.
	var step int

	// One "reasoned" line per Propose call, on every exit path — success,
	// decline, budget exhaustion, or any of the loop's error returns. err
	// and set are the named returns, so this sees the true terminal
	// outcome the same way the Store.Finish defer above already does.
	// Most error returns overwrite set with a fresh proposal.Set{} (see
	// each `return proposal.Set{}, ...` below), so set.Status is often
	// nil here — read defensively, never assume it reflects anything
	// real on an error path.
	defer func() {
		phase := ""
		if set.Status != nil {
			phase = set.Status.Phase
		}
		if err != nil {
			slog.Error("reasoned", "run_id", runID, "fingerprint", sig.Fingerprint, "step", step, "phase", phase, "err", err)
			return
		}
		slog.Info("reasoned", "run_id", runID, "fingerprint", sig.Fingerprint, "step", step, "phase", phase,
			"recommended", set.Recommended, "contractRef", set.ContractRefFor(set.Recommended),
			"proposals", len(set.Proposals), "evidence", len(set.Evidence),
			"gatePassed", set.Gate != nil && set.Gate.Passed, "reason", set.Status.Reason)
	}()

	var sao proposal.SAO
	if err := beat.Stage(ctx, e.tracer(), e.Stages, "assemble_sao", func(sctx context.Context) error {
		var err error
		sao, err = e.Intake.Assemble(sctx, sig)
		return err
	}); err != nil {
		return proposal.Set{}, fmt.Errorf("intake: %w", err)
	}

	set = proposal.Set{
		Name:        sig.Name,
		SignalRef:   sig.Fingerprint,
		SAOSnapshot: &sao,
		ServiceTier: sig.ServiceTier,
	}

	set.Status = &proposal.Status{}

	actions := e.Catalog.ApplicableToTier(sig.ServiceTier, sao)
	msgs := []Message{{Role: "user", Content: seedPrompt(sig, sao, e.FailureClasses, actions)}}
	var evidence []proposal.EvidenceRef
	proposed, declined := false, false

	for ; step < e.MaxSteps; step++ {
		var comp Completion
		if err := beat.Stage(ctx, e.tracer(), e.Stages, "llm_complete", func(sctx context.Context) error {
			var err error
			comp, err = e.Model.Complete(sctx, msgs, e.toolSpecs())
			return err
		}); err != nil {
			return proposal.Set{}, fmt.Errorf("model complete (step %d): %w", step, err)
		}
		msgs = append(msgs, comp.Message)

		if err := e.Store.Checkpoint(ctx, Turn{RunID: runID, Step: step, Msgs: msgs}); err != nil {
			return proposal.Set{}, fmt.Errorf("checkpoint (step %d): %w", step, err)
		}

		if len(comp.ToolCalls) == 0 {
			set.Status.Reason = "model ended turn without a tool call"
			declined = true
			break
		}

		done := false
		for _, call := range comp.ToolCalls {
			// One "tool_call" line per dispatched call — including the
			// terminal propose/insufficient calls, not just evidence tools
			// — so a live incident's tool-dispatch history reads straight
			// off kubectl logs instead of requiring an S3 transcript pull.
			slog.Info("tool_call", "run_id", runID, "fingerprint", sig.Fingerprint, "step", step, "tool", call.Name)
			switch call.Name {
			case "propose":
				var p proposal.Set
				if err := json.Unmarshal(call.Args, &p); err != nil {
					return proposal.Set{}, fmt.Errorf("decode propose: %w", err)
				}
				set.FailureClass = p.FailureClass
				set.Hypotheses = p.Hypotheses
				set.Proposals = p.Proposals
				proposed, done = true, true
			case "insufficient":
				var in insufficientInput
				if err := json.Unmarshal(call.Args, &in); err != nil {
					return proposal.Set{}, fmt.Errorf("decode insufficient: %w", err)
				}
				set.Status.Reason = in.Reason

				declined, done = true, true
			default:
				tool, ok := e.Tools[call.Name]
				if !ok {
					msgs = append(msgs, Message{Role: "tool", Content: fmt.Sprintf("unknown tool %q", call.Name)})
					continue
				}
				ref, err := tool.Run(ctx, call.Args)
				if err != nil {
					return proposal.Set{}, fmt.Errorf("tool %q: %w", call.Name, err)
				}
				evidence = append(evidence, ref)
				msgs = append(msgs, Message{Role: "tool", Content: ref.Summary})
			}
			if done {
				break
			}
		}
		if done {
			break
		}
	}
	set.Evidence = evidence

	if !proposed && !declined {
		set.Gate = &GateResult{BudgetOK: false, Reason: "budget"}
		set.Status.Phase = proposal.PhaseBudgetExhausted
		if err := e.Ledger.Record(ctx, set); err != nil {
			return proposal.Set{}, fmt.Errorf("record: %w", err)
		}
		return set, nil
	}
	if !proposed {
		if set.Status.Phase == "" {
			set.Status.Phase = proposal.PhaseNoAction
		}
		if err := e.Ledger.Record(ctx, set); err != nil {
			return proposal.Set{}, fmt.Errorf("record: %w", err)
		}
		return set, nil
	}

	if err := e.enforceCatalog(set, sao); err != nil {
		if !errors.Is(err, errClassMismatch) {
			return proposal.Set{}, err
		}
		set.Status.Phase = proposal.PhaseNoAction
		set.Status.Reason = err.Error()
		if err := e.Ledger.Record(ctx, set); err != nil {
			return proposal.Set{}, fmt.Errorf("record: %w", err)
		}
		return set, nil
	}

	if err := e.enforceCitations(set); err != nil {
		if !errors.Is(err, errUngroundedCitation) {
			return proposal.Set{}, err
		}

		set.Status.Phase = proposal.PhaseNoAction
		set.Status.Reason = err.Error()
		if err := e.Ledger.Record(ctx, set); err != nil {
			return proposal.Set{}, fmt.Errorf("record: %w", err)
		}
		return set, nil
	}

	enrichFromCatalog(e.Catalog, set.Proposals)

	_ = beat.Stage(ctx, e.tracer(), e.Stages, "causal_score", func(context.Context) error {
		set.CausalScores = e.Scorer.Score(set.SignalRef, sao.Change, sao.Topology, e.Weights)
		return nil
	})

	_ = beat.Stage(ctx, e.tracer(), e.Stages, "score_confidence", func(context.Context) error {
		scoreConfidences(&set, sao, e.Prior, sig.Fingerprint, e.Weights)
		return nil
	})

	ranked, why := e.Ranker.Rank(set.Proposals, sig.Impact.BlastRadius.Velocity)
	set.Proposals = ranked
	set.RankingRationale = &why
	if len(ranked) > 0 {
		set.Recommended = ranked[0].ID
	}

	openDupes, err := e.Ledger.Open(ctx, sig.Fingerprint, time.Now().Add(-e.DedupeWindow))
	if err != nil {
		return proposal.Set{}, fmt.Errorf("open dupes: %w", err)
	}
	var gate GateResult
	_ = beat.Stage(ctx, e.tracer(), e.Stages, "gate_eval", func(context.Context) error {
		gate = e.Gate.Evaluate(set, openDupes)
		return nil
	})
	set.Gate = &gate
	if set.Gate.Passed {
		set.Status.Phase = proposal.PhaseProposed
	} else {
		set.Status.Phase = proposal.PhaseNoAction
		set.Status.Reason = gate.Reason
	}

	if err := e.Ledger.Record(ctx, set); err != nil {
		return proposal.Set{}, fmt.Errorf("record: %w", err)
	}
	if set.Gate.Passed && e.Pub != nil {
		if err := e.Pub.Publish(ctx, "thump.proposals", set); err != nil {
			return proposal.Set{}, fmt.Errorf("publish: %w", err)
		}
	}

	return set, nil
}

func (e *Engine) toolSpecs() []ToolSpec {
	specs := make([]ToolSpec, 0, len(e.Tools)+2)
	for _, t := range e.Tools {
		specs = append(specs, t.Spec())
	}
	// The model can only call a tool it was offered, so the two terminal control
	// verbs must be real, offered tools — not bare switch arms. Catalogued actions
	// are deliberately NOT offered: the model names one by ref inside propose's
	// args, where enforceCatalog gates it.
	return append(specs, ProposeToolSpec(), InsufficientToolSpec())
}

func (e *Engine) enforceCatalog(set proposal.Set, sao proposal.SAO) error {
	allowed := make(map[string]bool)
	for _, c := range e.Catalog.Applicable(set.FailureClass, set.ServiceTier, sao) {
		allowed[c.Name] = true
	}
	for _, cand := range set.Proposals {
		if allowed[cand.ContractRef] {
			continue
		}
		if _, ok := e.Catalog.ByName(cand.ContractRef); ok {
			return fmt.Errorf("%w: %q does not apply to declared class %q", errClassMismatch, cand.ContractRef,
				set.FailureClass)
		}
		return fmt.Errorf("%w: %q", contract.ErrOutsideCatalog, cand.ContractRef)
	}
	return nil
}

func (e *Engine) enforceCitations(set proposal.Set) error {
	gathered := make(map[string]bool, len(set.Evidence))
	for _, ev := range set.Evidence {
		if ev.Query != "" {
			gathered[ev.Query] = true
		}
	}

	for _, cand := range set.Proposals {
		if len(cand.Citations) == 0 {
			return fmt.Errorf("%w: candidate %q carries no citations", errUngroundedCitation, cand.ID)
		}

		for _, cite := range cand.Citations {
			if !gathered[cite] {
				return fmt.Errorf("%w: %q", errUngroundedCitation, cite)
			}
		}
	}
	return nil
}

func seedPrompt(sig signal.Detection, sao proposal.SAO, classes []contract.FailureClassDefinition, actions []contract.ActionContract) string {
	var b strings.Builder
	subject := sig.OriginService
	if subject == "" {
		subject = sig.Name
	}
	fmt.Fprintf(&b, "signal on %s (severity %.0f%%, blast %.0f%%); investigate with the read-only tools, then call propose with your hypotheses and a candidate action — or insufficient if the evidence supports no action.\n",
		subject, sao.Signal.Severity.DegradationPct*100, sao.Signal.BlastRadius.AffectedPct*100)

	if len(sao.Topology.Upstream) > 0 || len(sao.Topology.Downstream) > 0 {
		b.WriteString("observed topology:\n")
		for _, n := range sao.Topology.Upstream {
			fmt.Fprintf(&b, "- upstream %s: %s\n", n.Name, n.State)
		}
		for _, n := range sao.Topology.Downstream {
			fmt.Fprintf(&b, "- downstream %s: %s\n", n.Name, n.State)
		}
	}

	if len(classes) > 0 {
		b.WriteString("failure classes — pick the one the EVIDENCE supports, not the one that has a matching action:\n")
		for _, d := range classes {
			fmt.Fprintf(&b, "- %s: %s\n", d.Class, d.Description)
		}
	}

	if len(actions) == 0 {
		b.WriteString("no catalogued action applies to this signal; if the evidence supports acting you must still call insufficient.")
		return b.String()
	}

	b.WriteString("you may ONLY propose an action from this catalog — use the exact contractRef:\n")
	for _, c := range actions {
		classNames := make([]string, len(c.ApplicableFailureClasses))
		for i, fc := range c.ApplicableFailureClasses {
			classNames[i] = string(fc)
		}
		if c.Action.Description != "" {
			fmt.Fprintf(&b, "- %s — %s (applies to: %s)\n", c.Name, c.Action.Description, strings.Join(classNames, ", "))
		} else {
			fmt.Fprintf(&b, "- %s (applies to: %s)\n", c.Name, strings.Join(classNames, ", "))
		}
	}
	return b.String()
}

func enrichFromCatalog(cat *contract.StaticCatalog, proposals []proposal.Candidate) {
	for i := range proposals {
		c, ok := cat.ByName(proposals[i].ContractRef)
		if !ok {
			continue
		}
		proposals[i].BlastTier = c.BlastTier // stamp proposal with BlastTier
		if c.SuccessCriteria.SeverityReductionPct != 0 {
			// The forecast half of the effectiveness delta: an authored
			// per-action expectation, copied like BlastTier — never invented
			// here, and left absent (not zero) when the catalog forecasts
			// nothing, so an unforecast action feeds no false effectiveness win.
			if proposals[i].PredictedImpact == nil {
				proposals[i].PredictedImpact = &proposal.PredictedImpact{}
			}
			proposals[i].PredictedImpact.SeverityReductionPct = c.SuccessCriteria.SeverityReductionPct
		}
		if c.Reversal.Method != "" {
			proposals[i].ReversalPath = &proposal.ReversalPath{
				Method:   c.Reversal.Method,
				Watching: c.SuccessCriteria.Metric,
				Trigger:  c.SuccessCriteria.Target,
			}
			proposals[i].GovernanceLevel = &proposal.GovernanceLevel{Band: string(decision.BandActReversible)}
		} else {
			proposals[i].GovernanceLevel = &proposal.GovernanceLevel{Band: string(decision.BandActDisruptive)}
		}
	}
}
