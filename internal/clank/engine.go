package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
)

type Engine struct {
	Intake       *Intake
	Model        Model
	Tools        map[string]Tool
	Catalog      *contract.StaticCatalog
	Ranker       *Ranker
	Gate         ReadinessGate
	Store        Store
	Scorer       CausalScorer
	DedupeWindow time.Duration
	Ledger       *MemProposalLog
	Pub          publish.Publisher[proposal.Set]
	MaxSteps     int
	Weights      ScoringWeights
}

func (e *Engine) Propose(ctx context.Context, sig signal.Detection) (proposal.Set, error) {
	sao, err := e.Intake.Assemble(ctx, sig)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("intake: %w", err)
	}

	set := proposal.Set{
		Name:        sig.Name,
		SignalRef:   sig.Fingerprint,
		SAOSnapshot: &sao,
		ServiceTier: sig.ServiceTier,
	}

	set.Status = &proposal.Status{}

	actions := e.Catalog.ApplicableToTier(sig.ServiceTier, sao)
	msgs := []Message{{Role: "user", Content: seedPrompt(sig, sao, actions)}}
	var evidence []proposal.EvidenceRef
	proposed, declined := false, false

	for step := 0; step < e.MaxSteps; step++ {
		comp, err := e.Model.Complete(ctx, msgs, e.toolSpecs())
		if err != nil {
			return proposal.Set{}, fmt.Errorf("model complete (step %d): %w", step, err)
		}
		msgs = append(msgs, comp.Message)

		if err := e.Store.Checkpoint(ctx, Turn{RunID: sig.Fingerprint, Step: step, Msgs: msgs}); err != nil {
			return proposal.Set{}, fmt.Errorf("checkpoint (step %d): %w", step, err)
		}

		if len(comp.ToolCalls) == 0 {
			set.Status.Reason = "model ended turn without a tool call"
			declined = true
			break
		}

		done := false
		for _, call := range comp.ToolCalls {
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
		return proposal.Set{}, err
	}

	enrichFromCatalog(e.Catalog, set.Proposals)

	set.CausalScores = e.Scorer.Score(set.SignalRef, sao.Change, sao.Topology, e.Weights)

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
	gate := e.Gate.Evaluate(set, openDupes)
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
		if !allowed[cand.ContractRef] {
			return fmt.Errorf("%w: %q", contract.ErrOutsideCatalog, cand.ContractRef)
		}
	}
	return nil
}

func seedPrompt(sig signal.Detection, sao proposal.SAO, actions []contract.ActionContract) string {
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

func enrichFromCatalog(cat *contract.StaticCatalog, proposals []proposal.Candidate) {
	for i := range proposals {
		c, ok := cat.ByName(proposals[i].ContractRef)
		if !ok {
			continue
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
