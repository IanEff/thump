package clank

// NewLoopForTest is the one deliberate crack in the package boundary: it lets
// clank_test build a loop through the exact same newLoop Main uses, without
// newLoop itself (or the unexported loop type it returns) becoming part of
// clank's real API. Only compiled under `go test` — the _test.go suffix means
// it never ships in the production binary.
func NewLoopForTest(model Model, tools map[string]Tool, intake *Intake, cat *StaticCatalog, outbox, outcomes string) *loop {
	return newLoop("", outbox, outcomes, model, tools, intake, cat)
}
