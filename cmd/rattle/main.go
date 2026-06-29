package main

import (
	"fmt"
)

// version information populated by ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	fmt.Printf("rattle %s, commit %s, built at %s\n", version, commit, date)
	// TODO: implement rattle logic
}
