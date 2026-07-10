package clank

import (
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
)

// loop is the offline (dir-poll) composition root: one Engine and one
// ReturnEdge sharing a single ledger and case base, so a proposal recorded on
// the forward path is the same open set the return edge closes.
type loop struct {
	Engine       *Engine
	ReturnEdge   *ReturnEdge
	Cases        *CaseBase
	OutcomeInbox string
}

func newLoop(_, outbox, outcomes string, model Model, tools map[string]Tool, intake *Intake, cat *contract.StaticCatalog, store Store, tracer trace.Tracer, stages *beat.StageRecorder) *loop {
	ledger := NewMemProposalLog() // ONE ledger
	cases := NewCaseBase()        // ONE case base
	eng := &Engine{
		Intake:       intake,
		Model:        model,
		Tools:        tools,
		Catalog:      cat,
		Ranker:       NewRanker(),
		Store:        store,
		Scorer:       &CausalScorerImpl{Prior: cases}, // scorer reads THIS case base
		DedupeWindow: time.Hour,
		Ledger:       ledger, // engine records into THIS ledger
		Pub:          &publish.DirPublisher[proposal.Set]{Dir: outbox, Name: proposalFilename},
		Gate:         ReadinessGate{},
		MaxSteps:     8,
		Tracer:       tracer,
		Stages:       stages,
	}
	re := &ReturnEdge{
		Inbox: outcomes, // thump's outbox — NOT outbox, which is hiss's inbox
		Click: Click{Ledger: ledger, Cases: cases},
	}
	return &loop{Engine: eng, ReturnEdge: re, Cases: cases, OutcomeInbox: outcomes}
}

// newBrokerEngine builds the broker-mode Engine: same shape as newLoop's, but
// publishing to the passed WAL/JetStream publisher instead of a directory, and
// sharing the caller's ledger and case base with the return-edge subscriber.
func newBrokerEngine(model Model, intake *Intake, store Store, tools map[string]Tool, pub publish.Publisher[proposal.Set], ledger *MemProposalLog, cases *CaseBase, tracer trace.Tracer, stages *beat.StageRecorder) *Engine {
	return &Engine{
		Intake:       intake,
		Model:        model,
		Tools:        tools,
		Catalog:      contract.Default(),
		Ranker:       NewRanker(),
		Store:        store,
		Scorer:       &CausalScorerImpl{Prior: cases},
		DedupeWindow: time.Hour,
		Ledger:       ledger,
		Pub:          pub,
		Gate:         ReadinessGate{},
		MaxSteps:     8,
		Tracer:       tracer,
		Stages:       stages,
	}
}
