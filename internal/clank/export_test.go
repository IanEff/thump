package clank

import (
	"context"
	"time"

	"github.com/ianeff/thump/internal/publish"
)

// NewLoopForTest is the one deliberate crack in the package boundary: it lets
// clank_test build a loop through the exact same newLoop Main uses, without
// newLoop itself (or the unexported loop type it returns) becoming part of
// clank's real API. Only compiled under `go test` — the _test.go suffix means
// it never ships in the production binary.
func NewLoopForTest(model Model, tools map[string]Tool, intake *Intake, cat *StaticCatalog, outbox, outcomes string, store Store) *loop {
	return newLoop("", outbox, outcomes, model, tools, intake, cat, store)
}

// DefaultCatalogForTest exposes the production catalog (the one Main wires)
// to clank_test, so the golden-path suite proves the loop against the SAME
// actions clank actually ships — not a bespoke test catalog that begs the
// question. Test-only, like the rest of this file.
func DefaultCatalogForTest() *StaticCatalog {
	return defaultCatalog()
}

// RunLoopForTest exposes Main's ticker-driven runLoop so a test can cancel a
// context it controls and assert a prompt return, without touching Main
// itself (which builds its context from OS signals).
func RunLoopForTest(ctx context.Context, tr *Transport, re *ReturnEdge) {
	runLoop(ctx, tr, re)
}

// NextDelayForTest exposes runLoop's backoff-growth decision so a test can
// pin grow/cap/reset behavior as a pure function, without racing a real
// timer. Jitter is deliberately NOT part of nextDelay (see clank.go) — it's
// added in runLoop after this decision, since a random value can't be
// pinned to a table test's "want".
func NextDelayForTest(cur time.Duration, tickOK bool) time.Duration {
	return nextDelay(cur, tickOK)
}

// NewBrokerEngineForTest exposes the broker-mode Engine construction to tests.
func NewBrokerEngineForTest(model Model, intake *Intake, store Store, tools map[string]Tool, pub *publish.WALPublisher[ProposalSet], ledger *MemProposalLog, cases *CaseBase) *Engine {
	return newBrokerEngine(model, intake, store, tools, pub, ledger, cases)
}

// TODO: These are a gooney workaround and this stuff should probably go elsewhere or be relagated to the dustbin of bad ideas.
func (l *MemProposalLog) SeedForTest(ps ProposalSet, age time.Duration) {
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
