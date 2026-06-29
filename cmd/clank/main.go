package main

import (
	"os"

	"github.com/ianeff/clank/internal/clank"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(clank.Main(os.Args[1:], os.Stdout, os.Stderr, version, commit, date))
}
