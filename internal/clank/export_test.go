package clank

import (
	"time"

	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
)

// NewLoopForTest is the one deliberate crack in the package boundary: it lets
// clank_test build a loop through the exact same newLoop Main uses, without
// newLoop itself (or the unexported loop type it returns) becoming part of
// clank's real API. Only compiled under `go test` — the _test.go suffix means
// it never ships in the production binary. Tracing isn't what these tests are
// about, so it's wired to a noop.Tracer{} here rather than making every call
// site pass one.
func NewLoopForTest(model Model, tools map[string]Tool, intake *Intake, cat *contract.StaticCatalog, outbox, outcomes string, store Store) *loop {
	return newLoop("", outbox, outcomes, model, tools, intake, cat, store, noop.Tracer{}, nil)
}

// DefaultCatalogForTest exposes the production catalog (the one Main wires)
// to clank_test, so the golden-path suite proves the loop against the SAME
// actions clank actually ships — not a bespoke test catalog that begs the
// question. Test-only, like the rest of this file.
func DefaultCatalogForTest() *contract.StaticCatalog {
	return contract.Default()
}

// NewBrokerEngineForTest exposes the broker-mode Engine construction to tests.
func NewBrokerEngineForTest(model Model, intake *Intake, store Store, tools map[string]Tool, pub publish.Publisher[proposal.Set], ledger *MemProposalLog, cases *CaseBase) *Engine {
	return newBrokerEngine(model, intake, store, tools, pub, ledger, cases, noop.Tracer{}, nil)
}

// TODO: These are a gooney workaround and this stuff should probably go elsewhere or be relagated to the dustbin of bad ideas.
func (l *MemProposalLog) SeedForTest(ps proposal.Set, age time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Backdate the record to simulate a stale entry
	l.sets = append(l.sets, recorded{set: ps, at: time.Now().Add(-age)})
}

func (l *MemProposalLog) LenForTest() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.sets)
}

func (cb *CaseBase) SetCasesForTest(cases []Case) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.cases = cases
}
