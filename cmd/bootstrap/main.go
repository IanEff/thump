// Command bootstrap provisions the shared JetStream stream and its durable
// consumers, then exits. It runs as a one-shot Job under its own identity —
// the only one granted $JS.API.> — so no long-running beat needs authority to
// reshape the topology it consumes.
package main

import (
	"os"

	"github.com/ianeff/thump/internal/bootstrap"
)

func main() {
	os.Exit(bootstrap.Main(os.Args[1:], os.Stderr))
}
