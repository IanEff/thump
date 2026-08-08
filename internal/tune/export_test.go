package tune

// DecideForTest exposes decide to tune_test — the bracket-finder Main calls
// after a sweep, reached through the one sanctioned crack per AGENTS.md §1.
func DecideForTest(points []Point) (Proposal, NotYet) {
	return decide(points)
}
