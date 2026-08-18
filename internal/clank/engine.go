package clank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	"github.com/ianeff/thump/internal/reason"
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
	Model          reason.Model                      // the LLM seam, faked in tests
	Tools          map[string]reason.Tool            // read-only evidence tools, keyed by the name the model calls
	Catalog        *contract.StaticCatalog           // the autonomy boundary: enforceCatalog rejects any proposed ContractRef this doesn't list
	FailureClasses []contract.FailureClassDefinition // authored, rig-invariant meaning of each class, rendered into seedPrompt; nil renders no block, so a bare-bones test Engine still works
	Ranker         *Ranker                           // orders the formed candidates once, after the loop exits
	Gate           ReadinessGate                     // budget ∧ dedup ∧ evidence, evaluated once on the formed set
	Store          Store                             // loop memory: one checkpoint per turn, a different lifetime from Ledger
	Scorer         CausalScorer                      // rates each change event's likelihood of causing the signal
	Prior          Prior                             // scoreConfidence's corroboration read — the same case base CausalScorerImpl.Prior points at; Engine needs its own reference because CausalScorer never exposes the one it holds
	DedupeWindow   time.Duration                     // how far back Ledger.Open looks for a live set on the same fingerprint
	Ledger         *MemProposalLog                   // every Propose run is recorded here, gated or not — the audit trail
	Recorder       *Recorder                         // counts reason loops the dedupe precheck stopped; nil-safe, same discipline as Tracer
	Pub            publish.Publisher[proposal.Set]   // delivery — only called when the gate passes
	Journal        publish.Publisher[proposal.Set]   // records every terminal phase, gated or not — never reaches the broker
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

// journal records set to Journal, matching Ledger.Record's scope exactly —
// every terminal phase, gated or not — rather than Pub's, which only ever
// sees a gate-passing set. Passes no subject: Journal is a JournalPublisher,
// which never routes on one, and a "thump."-prefixed literal here would read
// to internal/broker's grants scanner as a real broker subject that needs a
// nats.conf grant, when the whole point is that this record never reaches
// the broker at all.
func (e *Engine) journal(ctx context.Context, set proposal.Set) error {
	if e.Journal == nil {
		return nil
	}
	if err := e.Journal.Publish(ctx, "", set); err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	return nil
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
// published through Pub when the gate passes.
//
// A detection whose fingerprint already has a live set in the Ledger returns
// a zero Set and a nil error without reaching the model at all — no
// transcript, no ledger entry, no delivery. The run is counted and logged
// rather than recorded, because the set already answering that fingerprint is
// the audit unit and a second one would only record that the transport
// delivered twice.
func (e *Engine) Propose(ctx context.Context, sig signal.Detection) (set proposal.Set, err error) {
	// The gate's dedup minimum runs on the formed set, which is the right
	// place to decide whether a set is worth delivering and the wrong place to
	// discover the work was never worth doing: reaching it costs several model
	// calls and every tool they issue. The same question asked here is free,
	// and a live set for this fingerprint means the answer already exists —
	// so the run stops before the first token rather than paying to rebuild
	// something the ledger is already holding.
	open, err := e.Ledger.Open(ctx, sig.Fingerprint, time.Now().Add(-e.DedupeWindow))
	if err != nil {
		return proposal.Set{}, fmt.Errorf("open dupes: %w", err)
	}
	if len(open) > 0 {
		if e.Recorder != nil {
			e.Recorder.recordRedeliverySkipped()
		}
		slog.Info("redelivery skipped", "fingerprint", sig.Fingerprint, "open_sets", len(open))
		return proposal.Set{}, nil
	}

	// runID, not sig.Fingerprint alone, keys every Store call below — two
	// invocations for the same fingerprint (a legitimate re-fire after
	// rattle's debounce window, a JetStream redelivery, a retry after a
	// transient error) must never share checkpoint objects, or the second
	// run silently clobbers the first's transcript at each matching step.
	runID := fmt.Sprintf("%s/%d", sig.Fingerprint, time.Now().UnixNano())
	defer func() {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if finishErr := e.Store.Finish(finishCtx, runID, err); finishErr != nil {
			slog.Error("clank: record terminal store record failed", "run_id", runID, "err", finishErr)
		}
	}()

	// step is hoisted out of the reason loop below (`for ; step < ...`
	// instead of `for step := 0; ...`) so the terminal log deferred next
	// can report how far the run got on every exit path, not just the
	// ones lexically inside the loop.
	var step int

	// usage accumulates every turn's token counts, not just the last one —
	// the reasoned line reports what the whole Propose call was billed for.
	var usage reason.Usage

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
			slog.Error("reasoned", "run_id", runID, "fingerprint", sig.Fingerprint, "step", step, "phase", phase, "err", err,
				"input_tokens", usage.InputTokens, "cache_creation_input_tokens", usage.CacheCreationInputTokens,
				"cache_read_input_tokens", usage.CacheReadInputTokens)
			return
		}
		causal := summarizeCausal(set.CausalScores)
		terms := set.TermsFor(set.Recommended)
		slog.Info("reasoned",
			"run_id", runID,
			"fingerprint", sig.Fingerprint,
			"step", step,
			"phase", phase,
			"recommended", set.Recommended,
			"contractRef", set.ContractRefFor(set.Recommended),
			"proposals", len(set.Proposals),
			"evidence", len(set.Evidence),
			"gatePassed", set.Gate != nil && set.Gate.Passed,
			"reason", set.Status.Reason,
			"confidence", set.ConfidenceFor(set.Recommended),
			"computedConfidence", set.ComputedConfidenceFor(set.Recommended),
			"ceilingBound", set.ConfidenceCeilingBoundFor(set.Recommended),
			"signalConfidence", terms.SignalConfidence,
			"corroborated", terms.Corroborated,
			"grounding", terms.Grounding,
			"alignmentOK", terms.AlignmentOK,
			"likelihoodOK", terms.LikelihoodOK,
			"maxLikelihood", causal.maxLikelihood,
			"inTopology", causal.inTopology,
			"outOfTopology", causal.outOfTopology,
			"input_tokens", usage.InputTokens,
			"cache_creation_input_tokens", usage.CacheCreationInputTokens,
			"cache_read_input_tokens", usage.CacheReadInputTokens)
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
		RunID:       runID,
		SignalRef:   sig.Fingerprint,
		SLORef:      sig.SLORef,
		SAOSnapshot: &sao,
		ServiceTier: sig.ServiceTier,
	}

	set.Status = &proposal.Status{}

	actions := e.Catalog.ApplicableToTier(sig.ServiceTier, sao)

	msgs := []reason.Message{{Role: "user", Content: seedPrompt(sig, sao, e.FailureClasses, actions)}}
	var evidence []proposal.EvidenceRef
	proposed, declined := false, false

	for ; step < e.MaxSteps; step++ {
		var comp reason.Completion
		if err := beat.Stage(ctx, e.tracer(), e.Stages, "llm_complete", func(sctx context.Context) error {
			var err error
			comp, err = e.Model.Complete(sctx, msgs, e.toolSpecs())
			return err
		}); err != nil {
			return proposal.Set{}, fmt.Errorf("model complete (step %d): %w", step, err)
		}
		comp.Message.ToolCalls = withCallIDs(comp.ToolCalls, step)
		msgs = append(msgs, comp.Message)
		usage.InputTokens += comp.Usage.InputTokens
		usage.CacheCreationInputTokens += comp.Usage.CacheCreationInputTokens
		usage.CacheReadInputTokens += comp.Usage.CacheReadInputTokens

		if err := e.Store.Checkpoint(ctx, Turn{RunID: runID, Step: step, Msgs: msgs}); err != nil {
			return proposal.Set{}, fmt.Errorf("checkpoint (step %d): %w", step, err)
		}

		if len(comp.ToolCalls) == 0 {
			set.Status.Reason = "model ended turn without a tool call"
			declined = true
			break
		}

		done := false
		var results []reason.ToolResult
		for _, call := range comp.ToolCalls {
			// One "tool_call" line per dispatched call — including the
			// terminal propose/insufficient calls, not just evidence tools
			// — so a live incident's tool-dispatch history reads straight
			// off kubectl logs instead of requiring an S3 transcript pull.
			slog.Info("tool_call", "run_id", runID, "fingerprint", sig.Fingerprint, "step", step, "tool", call.Name)
			switch call.Name {
			case "propose":
				var p proposeInput
				if err := json.Unmarshal(call.Args, &p); err != nil {
					return proposal.Set{}, fmt.Errorf("decode propose: %w", err)
				}
				set.FailureClass = p.FailureClass
				set.Hypotheses = p.Hypotheses
				set.Proposals = p.candidates()
				proposed, done = true, true
			case "insufficient":
				var in insufficientInput
				if err := json.Unmarshal(call.Args, &in); err != nil {
					return proposal.Set{}, fmt.Errorf("decode insufficient: %w", err)
				}
				set.Status.Reason = in.Reason
				// A decline may still carry a diagnosis — recorded so the
				// ledger shows which classes accumulate no-remedy declines.
				set.FailureClass = in.FailureClass

				declined, done = true, true
			default:
				tool, ok := e.Tools[call.Name]
				if !ok {
					results = append(results, reason.ToolResult{
						CallID:  call.ID,
						Name:    call.Name,
						Digest:  fmt.Sprintf("unknown tool %q", call.Name),
						IsError: true,
					})
					continue
				}
				var ref proposal.EvidenceRef
				if err := beat.Stage(ctx, e.tracer(), e.Stages, "tool:"+call.Name, func(sctx context.Context) error {
					var err error
					ref, err = tool.Run(sctx, call.Args)
					return err
				}); err != nil {
					return proposal.Set{}, fmt.Errorf("tool %q: %w", call.Name, err)
				}
				// A tool that can name its own evidence in a form the model
				// can retype verbatim sets Key itself; one that can't (a
				// raw kube call's args, a constructed LogQL string) leaves
				// it empty and the engine assigns a short, stable label
				// instead.
				if ref.Key == "" {
					ref.Key = evidenceKey(call.Name, len(evidence))
				}
				evidence = append(evidence, ref)
				// The digest carries its citable key visibly: enforceCitations
				// grades citations against EvidenceRef.Key by exact match, so
				// the conversation must show the model the exact string it will
				// be graded on — a summary alone leaves the key unguessable.
				content := ref.Summary
				if ref.Key != "" {
					content = fmt.Sprintf("%s [cite: %s]", ref.Summary, ref.Key)
				}
				results = append(results, reason.ToolResult{CallID: call.ID, Name: call.Name, Digest: content})
			}
			if done {
				break
			}
		}
		// One message per turn, never one per call: splitting a parallel turn's
		// results across messages teaches the model to stop making parallel
		// calls, and every live turn so far has issued two to five.
		if len(results) > 0 {
			msgs = append(msgs, reason.Message{Role: "tool", ToolResults: results})
		}
		if done {
			break
		}
	}
	set.Evidence = evidence

	if !proposed && !declined {
		set.Gate = &proposal.GateResult{BudgetOK: false, Reason: "budget"}
		set.Status.Phase = proposal.PhaseBudgetExhausted
		if err := e.Ledger.Record(ctx, set); err != nil {
			return proposal.Set{}, fmt.Errorf("record: %w", err)
		}
		if err := e.journal(ctx, set); err != nil {
			return proposal.Set{}, err
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
		if err := e.journal(ctx, set); err != nil {
			return proposal.Set{}, err
		}
		return set, nil
	}

	if err := e.enforceCatalog(set, sao); err != nil {
		// Both refusal shapes — a real action mismatched to the declared class
		// and a ref the catalog has never heard of — decline auditably: the
		// autonomy boundary still holds (nothing is delivered), but the refusal
		// lands in the ledger instead of erroring the run into an unrecorded
		// hole dedup can't see.
		if !errors.Is(err, errClassMismatch) && !errors.Is(err, contract.ErrOutsideCatalog) {
			return proposal.Set{}, err
		}
		set.Status.Phase = proposal.PhaseNoAction
		set.Status.Reason = err.Error()
		if err := e.Ledger.Record(ctx, set); err != nil {
			return proposal.Set{}, fmt.Errorf("record: %w", err)
		}
		if err := e.journal(ctx, set); err != nil {
			return proposal.Set{}, err
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
		if err := e.journal(ctx, set); err != nil {
			return proposal.Set{}, err
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

	// Not redundant with the precheck at the top: the reason loop above takes
	// as long as the model does, and a set for this fingerprint can be
	// recorded while it runs. The precheck is a cost saver; this is the gate's
	// dedup minimum, and the gate is a conjunction — dropping one minimum
	// because another check happens to cover the common case is exactly the
	// blend the gate refuses to be.
	openDupes, err := e.Ledger.Open(ctx, sig.Fingerprint, time.Now().Add(-e.DedupeWindow))
	if err != nil {
		return proposal.Set{}, fmt.Errorf("open dupes: %w", err)
	}
	var gate proposal.GateResult
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
	if err := e.journal(ctx, set); err != nil {
		return proposal.Set{}, err
	}
	if set.Gate.Passed && e.Pub != nil {
		if err := e.Pub.Publish(ctx, "thump.proposals", set); err != nil {
			return proposal.Set{}, fmt.Errorf("publish: %w", err)
		}
	}

	return set, nil
}

// causalSummary is the causal scoring detail the reasoned line carries, so a
// live run's Likelihood term is readable from kubectl logs instead of only from
// a sealed ProposalSet in the object store.
type causalSummary struct {
	maxLikelihood float64 // the strongest in-topology score, which is the only one confidence reads
	inTopology    int
	outOfTopology int
}

// summarizeCausal reduces a run's CausalScores to what a reader diagnosing the
// causal term needs. inTopology at zero with outOfTopology above it is the
// signature of a broken join: change events arrived and none of them named
// something the topology graph knows.
func summarizeCausal(scores []CausalScore) causalSummary {
	var s causalSummary
	for _, cs := range scores {
		if !cs.InTopology {
			s.outOfTopology++
			continue
		}
		s.inTopology++
		s.maxLikelihood = max(s.maxLikelihood, cs.Likelihood)
	}
	return s
}

func (e *Engine) toolSpecs() []reason.ToolSpec {
	specs := make([]reason.ToolSpec, 0, len(e.Tools)+2)
	for _, t := range e.Tools {
		specs = append(specs, t.Spec())
	}
	// The model can only call a tool it was offered, so the two terminal control
	// verbs must be real, offered tools — not bare switch arms. Catalogued actions
	// are deliberately NOT offered: the model names one by ref inside propose's
	// args, where enforceCatalog gates it.
	specs = append(specs, ProposeToolSpec(), InsufficientToolSpec())
	// e.Tools is a map, so its iteration order is randomized per call — sorted
	// here so the rendered request body is byte-identical turn to turn, which
	// is what lets a cache_control breakpoint on the tool catalog ever hit.
	slices.SortFunc(specs, func(a, b reason.ToolSpec) int { return strings.Compare(a.Name, b.Name) })
	return specs
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
		if ev.Key != "" {
			gathered[ev.Key] = true
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

	if sao.Signal.Metric != "" {
		fmt.Fprintf(&b, "signal on %s [%s] (confidence: %.2f, severity: %0.f%%, blast: %.0f%%); investigate with the read-only tools, then call propose with your hypotheses and a candidate action -- or insufficient if the evidence supports no action.\n",
			subject, sao.Signal.Metric, sao.Signal.Confidence, sao.Signal.Severity.DegradationPct*100, sao.Signal.BlastRadius.AffectedPct*100)
	} else {
		fmt.Fprintf(&b, "signal on %s (confidence %.2f, severity %.0f%%, blast %.0f%%); investigate with the read-only tools, then call propose with your hypotheses and a candidate action -- or insufficient if the evidence supports no action.\n",
			subject, sao.Signal.Confidence, sao.Signal.Severity.DegradationPct*100, sao.Signal.BlastRadius.AffectedPct*100)
	}

	if len(sao.Change.Events) > 0 {
		b.WriteString("recent change events:\n")

		for _, e := range sao.Change.Events {
			fmt.Fprintf(&b, "- %s change on %s (age %s seconds)\n", e.Type, e.Target, e.Age.Round(time.Second))
		}
	}

	if len(sao.Topology.Upstream) > 0 || len(sao.Topology.Downstream) > 0 {
		b.WriteString("observed topology:\n")
		for _, n := range sao.Topology.Upstream {
			fmt.Fprintf(&b, "- upstream %s: %s\n", n.Name, n.State)
		}
		for _, n := range sao.Topology.Downstream {
			fmt.Fprintf(&b, "- downstream %s: %s\n", n.Name, n.State)
		}
	}

	b.WriteString("evidence & confidence rules:\n")
	b.WriteString("- to propose an action, cite at least one LIVE telemetry result about the affected service, or a node in its declared topology.\n")
	b.WriteString("- a citation is the exact key shown as [cite: <key>] in a tool result, repeated verbatim — never a description of the value.\n")
	b.WriteString("- grounding counts DISTINCT tools, not citations: one live backend cited twice grounds no better than once. Corroborate across a second tool before proposing — a metric plus logs, or a metric plus cluster state.\n")
	b.WriteString("- state a confidence on every candidate. Confidence is two-sided: over-confidence risks an unsafe action, but under-confidence below the governance floor escalates to a human operator. The emitted number is computed from your citations' grounding; yours acts as a ceiling.\n")

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
	b.WriteString("if no catalogued action lists your diagnosed failure class, call insufficient and name the class — never reach for the nearest action.")
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
				Method:    c.Reversal.Method,
				Watching:  c.SuccessCriteria.Metric,
				Trigger:   c.SuccessCriteria.Target,
				Automatic: automaticReversal(c.Execution.Reverse),
			}
			proposals[i].GovernanceLevel = &proposal.GovernanceLevel{Band: string(decision.BandActReversible)}
		} else {
			proposals[i].GovernanceLevel = &proposal.GovernanceLevel{Band: string(decision.BandActDisruptive)}
		}
	}
}

// automaticReversal reports whether the engine can finish this contract's
// undo on its own. A maintenanceRelease step leaves a release for a
// reviewer to merge — nobody has landed anything until they do.
func automaticReversal(steps []contract.Step) bool {
	for _, s := range steps {
		if s.Verb == "maintenanceRelease" {
			return false
		}
	}
	return true
}

// evidenceKey is the citation key stamped onto a gathered EvidenceRef whose
// Query can't serve as one — short and engine-authored, so a later turn can
// retype it verbatim. n is the ref's position in the run's whole evidence
// list, not per-tool, so two calls to the same tool never collide.
func evidenceKey(tool string, n int) string {
	return fmt.Sprintf("%s-%d", tool, n)
}

// withCallIDs guarantees every call in a turn has an identifier its
// result can name.
func withCallIDs(calls []reason.ToolCall, step int) []reason.ToolCall {
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = fmt.Sprintf("call_%d_%d", step, i)
		}
	}
	return calls
}
