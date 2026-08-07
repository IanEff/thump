// Command clank runs the Reasoning beat: it turns one Detection into a ranked,
// evidence-backed ProposalSet through a bounded LLM loop. Its entire output is
// a document — it selects from the action catalog and never permits, and it
// holds no path to the cluster it reasons about.
package main

import (
	"os"

	"github.com/ianeff/thump/internal/clank"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(clank.Main(os.Args[1:], os.Stdout, os.Stderr, version, commit, date))
}
