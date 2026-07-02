package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ianeff/clank/internal/signal"
)

type Engine struct {
	Intake       *Intake
	Model        Model
	Tools        map[string]Tool
	Catalog      *StaticCatalog
	Ranker       *Ranker
	Gate         ReadinessGate
	Store        Store
	Scorer       CausalScorer
	DedupeWindow time.Duration
	Ledger       *MemProposalLog
	Sink         ProposalSink
	MaxSteps     int
	Policy       GatePolicy
}

func (e *Engine) Propose(ctx context.Context, sig signal.Detection) (ProposalSet, error) {
	sao, err := e.Intake.Assemble(ctx, sig)
	if err != nil {
		return ProposalSet{}, fmt.Errorf("intake: %w", err)
	}

	set := ProposalSet{
		Name:        sig.Name,
		SignalRef:   sig.Fingerprint,
		SAOSnapshot: &sao,
		ServiceTier: sig.ServiceTier,
	}

	set.Status = &ProposalStatus{}

	actions := e.Catalog.ApplicableToTier(sig.ServiceTier, sao)
	msgs := []Message{{Role: "user", Content: seedPrompt(sao, actions)}}
	var evidence []EvidenceRef
	proposed, declined := false, false

	for step := 0; step < e.MaxSteps; step++ {
		comp, err := e.Model.Complete(ctx, msgs, e.toolSpecs())
		if err != nil {
			return ProposalSet{}, fmt.Errorf("model complete (step %d): %w", step, err)
		}
		msgs = append(msgs, comp.Message)

		if err := e.Store.Checkpoint(ctx, Turn{RunID: sig.Fingerprint, Step: step, Msgs: msgs}); err != nil {
			return ProposalSet{}, fmt.Errorf("checkpoint (step %d): %w", step, err)
		}

		if len(comp.ToolCalls) == 0 {
			declined = true
			break
		}

		done := false
		for _, call := range comp.ToolCalls {
			switch call.Name {
			case "propose":
				var p ProposalSet
				if err := json.Unmarshal(call.Args, &p); err != nil {
					return ProposalSet{}, fmt.Errorf("decode propose: %w", err)
				}
				set.FailureClass = p.FailureClass
				set.Hypotheses = p.Hypotheses
				set.Proposals = p.Proposals
				proposed, done = true, true
			case "insufficient":
				declined, done = true, true
			default:
				tool, ok := e.Tools[call.Name]
				if !ok {
					msgs = append(msgs, Message{Role: "tool", Content: fmt.Sprintf("unknown tool %q", call.Name)})
					continue
				}
				ref, err := tool.Run(ctx, call.Args)
				if err != nil {
					return ProposalSet{}, fmt.Errorf("tool %q: %w", call.Name, err)
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
		set.Status.Phase = "budget_exhausted"
		if err := e.Ledger.Record(ctx, set); err != nil {
			return ProposalSet{}, fmt.Errorf("record: %w", err)
		}
		return set, nil
	}
	if !proposed {
		if set.Status.Phase == "" {
			set.Status.Phase = "no_action"
		}
		if err := e.Ledger.Record(ctx, set); err != nil {
			return ProposalSet{}, fmt.Errorf("record: %w", err)
		}
		return set, nil
	}

	if err := e.enforceCatalog(set, sao); err != nil {
		return ProposalSet{}, err
	}

	set.CausalScores = e.Scorer.Score(sao.Change, sao.Topology, e.Policy.CausalWeights)

	ranked, why := e.Ranker.Rank(set.Proposals, sig.Impact.BlastRadius.Velocity)
	set.Proposals = ranked
	set.RankingRationale = &why
	if len(ranked) > 0 {
		set.Recommended = ranked[0].ID
	}

	openDupes, err := e.Ledger.Open(ctx, sig.Fingerprint, time.Now().Add(-e.DedupeWindow))
	if err != nil {
		return ProposalSet{}, fmt.Errorf("open dupes: %w", err)
	}
	gate := e.Gate.Evaluate(set, openDupes, e.Policy)
	set.Gate = &gate
	if set.Gate.Passed {
		set.Status.Phase = "proposed"
	} else {
		set.Status.Phase = "no_action"
	}

	if err := e.Ledger.Record(ctx, set); err != nil {
		return ProposalSet{}, fmt.Errorf("record: %w", err)
	}
	if set.Gate.Passed && e.Sink != nil {
		if err := e.Sink.Deliver(ctx, set); err != nil {
			return ProposalSet{}, fmt.Errorf("deliver: %w", err)
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

func (e *Engine) enforceCatalog(set ProposalSet, sao SAO) error {
	allowed := make(map[string]bool)
	for _, c := range e.Catalog.Applicable(set.FailureClass, set.ServiceTier, sao) {
		allowed[c.Name] = true
	}
	for _, cand := range set.Proposals {
		if !allowed[cand.ContractRef] {
			return fmt.Errorf("%w: %q", ErrOutsideCatalog, cand.ContractRef)
		}
	}
	return nil
}

func seedPrompt(sao SAO, actions []ActionContract) string {
	var b strings.Builder
	fmt.Fprintf(&b, "signal on %s (severity %.0f%%, blast %.0f%%); investigate with the read-only tools, then call propose with your hypotheses and a candidate action — or insufficient if the evidence supports no action.\n",
		sao.Signal.Metric, sao.Signal.Severity.DegradationPct, sao.Signal.BlastRadius.AffectedPct)

	if len(actions) == 0 {
		b.WriteString("no catalogued action applies to this signal; if the evidence supports acting you must still call insufficient.")
		return b.String()
	}

	b.WriteString("you may ONLY propose an action from this catalog — use the exact contractRef:\n")
	for _, c := range actions {
		classes := make([]string, len(c.ApplicableFailureClasses))
		for i, fc := range c.ApplicableFailureClasses {
			classes[i] = string(fc)
		}
		if c.Action.Description != "" {
			fmt.Fprintf(&b, "- %s — %s (applies to: %s)\n", c.Name, c.Action.Description, strings.Join(classes, ", "))
		} else {
			fmt.Fprintf(&b, "- %s (applies to: %s)\n", c.Name, strings.Join(classes, ", "))
		}
	}
	return b.String()
}
