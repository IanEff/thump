package clank

import (
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
)

// loop is the offline (dir-poll) composition root: one Engine, one
// ReturnEdge, and one DeclineEdge sharing a single ledger and case base, so
// a proposal recorded on the forward path is the same open set either
// return edge closes.
type loop struct {
	Engine       *Engine
	ReturnEdge   *ReturnEdge
	DeclineEdge  *DeclineEdge
	Cases        *CaseBase
	OutcomeInbox string
}

func newLoop(_, outbox, outcomes, declines string, model Model, tools map[string]Tool, intake *Intake, cat *contract.StaticCatalog, classes []contract.FailureClassDefinition, store Store, dedupeWindow time.Duration, tracer trace.Tracer, stages *beat.StageRecorder) *loop {
	ledger := NewMemProposalLog() // ONE ledger
	cases := NewCaseBase()        // ONE case base
	eng := &Engine{
		Intake:         intake,
		Model:          model,
		Tools:          tools,
		Catalog:        cat,
		FailureClasses: classes,
		Ranker:         NewRanker(),
		Store:          store,
		Scorer:         &CausalScorerImpl{Prior: cases}, // scorer reads THIS case base
		Prior:          cases,                           // scoreConfidence reads the same case base
		DedupeWindow:   dedupeWindow,
		Ledger:         ledger, // engine records into THIS ledger
		Pub:            &publish.DirPublisher[proposal.Set]{Dir: outbox, Name: proposalFilename},
		Gate:           ReadinessGate{},
		MaxSteps:       8,
		Tracer:         tracer,
		Stages:         stages,
		Weights:        DefaultScoringWeights(),
	}
	re := &ReturnEdge{
		Inbox: outcomes, // thump's outbox — NOT outbox, which is hiss's inbox
		Click: Click{Ledger: ledger, Cases: cases},
	}
	de := &DeclineEdge{
		Inbox:  declines, // thump's outbox/declines — a governance non-approval, never an outcome
		Ledger: ledger,
	}
	return &loop{Engine: eng, ReturnEdge: re, DeclineEdge: de, Cases: cases, OutcomeInbox: outcomes}
}

// newBrokerEngine builds the broker-mode Engine: same shape as newLoop's, but
// publishing to the passed WAL/JetStream publisher instead of a directory, and
// sharing the caller's ledger and case base with the return-edge subscriber.
func newBrokerEngine(model Model, intake *Intake, store Store, tools map[string]Tool, cat *contract.StaticCatalog, classes []contract.FailureClassDefinition, pub publish.Publisher[proposal.Set], ledger *MemProposalLog, cases *CaseBase, dedupeWindow time.Duration, tracer trace.Tracer, stages *beat.StageRecorder) *Engine {
	return &Engine{
		Intake:         intake,
		Model:          model,
		Tools:          tools,
		Catalog:        cat,
		FailureClasses: classes,
		Ranker:         NewRanker(),
		Store:          store,
		Scorer:         &CausalScorerImpl{Prior: cases},
		Prior:          cases,
		DedupeWindow:   dedupeWindow,
		Ledger:         ledger,
		Pub:            pub,
		Gate:           ReadinessGate{},
		MaxSteps:       8,
		Tracer:         tracer,
		Stages:         stages,
		Weights:        DefaultScoringWeights(),
	}
}

// DefaultScoringWeights is the tuning both production constructors wire —
// uniform causal axes (no evidence yet favors recency over topology over
// history), a 30-day freshness half-life, and the grounding tiers the
// confidence scorer's regression table locks: 0.3 uncorroborated, 0.7 for
// one live in-topology citation, 1.0 for two or more. A zero-value
// ScoringWeights doesn't degrade — it multiplies whole scoring terms out
// of existence — so construction must never rely on the zero value.
func DefaultScoringWeights() ScoringWeights {
	return ScoringWeights{
		Temporal:          1.0 / 3,
		Topological:       1.0 / 3,
		Historical:        1.0 / 3,
		FreshnessHalfLife: 30 * 24 * time.Hour,
		GroundingNone:     0.3,
		GroundingOne:      0.7,
		GroundingMany:     1.0,
	}
}
