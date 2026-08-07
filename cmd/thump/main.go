// Command thump runs the Execution beat: it renders or executes an already
// approved Decision, watches for convergence, and fires the authored reversal
// when the success window closes unmet. It decides nothing — it acts only on a
// verdict hiss granted, and only while the global kill switch is armed.
package main

import (
	"os"

	"github.com/ianeff/thump/internal/notify/slack"
	"github.com/ianeff/thump/internal/thump"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(thump.Main(os.Args[1:], os.Stdout, os.Stderr, version, commit, date, newSlackNotifier))
}

// newSlackNotifier is the one place in the repo that constructs a concrete
// Slack client — internal/thump stays free of it, injected through the
// Notifier interface like Exec.
func newSlackNotifier(url string) thump.Notifier {
	return &slack.Webhook{URL: url}
}
