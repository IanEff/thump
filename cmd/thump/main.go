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
// Slack client — internal/thump can't do it itself (internal/notify/slack
// imports internal/thump for HeldAction, so the reverse import would cycle);
// this closure crosses that boundary from the outside, where package main is
// free to import both sides without one.
func newSlackNotifier(url string) thump.Notifier {
	return &slack.Webhook{URL: url}
}
