package main

import (
	"os"

	"github.com/ianeff/thump/internal/thump"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(thump.Main(os.Args[1:], os.Stdout, os.Stderr, version, commit, date))
}
