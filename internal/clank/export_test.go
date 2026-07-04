package clank

import (
	"context"
	"time"
)

// NewLoopForTest is the one deliberate crack in the package boundary: it lets
// clank_test build a loop through the exact same newLoop Main uses, without
// newLoop itself (or the unexported loop type it returns) becoming part of
// clank's real API. Only compiled under `go test` — the _test.go suffix means
// it never ships in the production binary.
func NewLoopForTest(model Model, tools map[string]Tool, intake *Intake, cat *StaticCatalog, outbox, outcomes string) *loop {
	return newLoop("", outbox, outcomes, model, tools, intake, cat)
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
