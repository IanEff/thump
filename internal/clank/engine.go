package clank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrOutsideCatalog = errors.New("clank: proposed contract not in catalog")

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

func (e *Engine) Propose(ctx context.Context, sig SignalDetection) (ProposalSet, error) {
	sao, err := e.Intake.Assemble(ctx, sig)
	if err != nil {
		return ProposalSet{}, fmt.Errorf("intake: %w", err)
	}

	set := ProposalSet{
		Name:        sig.Name,
		SignalRef:   sig.Fingerprint,
		SAOSnapshot: sao,
		ServiceTier: sig.ServiceTier,
	}

	msgs := []Message{{Role: "user", Content: seedPrompt(sao)}}
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
		set.Gate = GateResult{BudgetOK: false, Reason: "budget"}
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
	set.RankingRationale = why
	if len(ranked) > 0 {
		set.Recommended = ranked[0].ID
	}

	openDupes, err := e.Ledger.Open(ctx, sig.Fingerprint, time.Now().Add(-e.DedupeWindow))
	if err != nil {
		return ProposalSet{}, fmt.Errorf("open dupes: %w", err)
	}
	set.Gate = e.Gate.Evaluate(set, openDupes, e.Policy)
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
	specs := make([]ToolSpec, 0, len(e.Tools))
	for _, t := range e.Tools {
		specs = append(specs, t.Spec())
	}
	return specs
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

func seedPrompt(sao SAO) string {
	return fmt.Sprintf("signal on %s (severity %.0f%%, blast %.0f%%); investigate with read-only tools, then propose.", sao.Signal.Metric, sao.Signal.Severity.DegradationPct, sao.Signal.BlastRadius.AffectedPct)
}
