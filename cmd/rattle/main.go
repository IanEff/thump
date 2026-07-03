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
