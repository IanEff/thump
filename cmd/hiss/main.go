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
