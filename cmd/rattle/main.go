// Command rattle runs the Signal beat: it watches declared SLOs and emits a
// fingerprinted Detection describing what the metrics say. It never says what
// the divergence means — that judgment belongs to clank, one beat downstream.
package main

import (
	"os"

	"github.com/ianeff/thump/internal/rattle"
)

// version information populated by ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(rattle.Main(os.Args[1:], os.Stdout, os.Stderr, version, commit, date))
}
