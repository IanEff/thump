// Command hiss runs the Governance beat: it answers one question about a
// ProposalSet — allowed, right now? — and emits a single auditable Decision.
// It never re-ranks the set or substitutes a different candidate, and it is
// the only identity permitted to publish a verdict.
package main

import (
	"os"

	"github.com/ianeff/thump/internal/hiss"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(hiss.Main(os.Args[1:], os.Stdout, os.Stderr, version, commit, date))
}
