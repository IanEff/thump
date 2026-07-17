package thump

import (
	"context"
	"testing"

	"github.com/ianeff/thump/internal/config"
)

// stubNotifier is a bare-minimum Notifier for buildNotifier's own test — it
// never touches a network, unlike fakes_test.go's fakeNotifier (package
// thump_test, out of reach from here: this file is package thump so it can
// see the unexported buildNotifier).
type stubNotifier struct{}

func (stubNotifier) Notify(context.Context, HeldAction) error { return nil }

// buildNotifier can't construct *slack.Webhook itself — internal/notify/slack
// imports internal/thump for HeldAction, so the reverse import would cycle.
// ctor is supplied by cmd/thump's composition root, which is free to import
// the Slack package; buildNotifier's own job is just "empty URL means nil,
// non-empty means call ctor" — so the test proves exactly that, with a ctor
// that carries no dependency of its own.
func TestBuildNotifier_URLSetCallsCtor(t *testing.T) {
	t.Parallel()
	var gotURL string
	n := buildNotifier(config.Thump{SlackWebhookURL: "https://hooks.slack.example/T000/B000/xxx"}, func(url string) Notifier {
		gotURL = url
		return stubNotifier{}
	})
	if n == nil {
		t.Fatal("a configured SLACK_WEBHOOK_URL must build a non-nil Notifier")
	}
	if gotURL != "https://hooks.slack.example/T000/B000/xxx" {
		t.Errorf("ctor called with %q, want the configured URL", gotURL)
	}
}

func TestBuildNotifier_UnsetNeverCallsCtor(t *testing.T) {
	t.Parallel()
	called := false
	n := buildNotifier(config.Thump{}, func(string) Notifier {
		called = true
		return stubNotifier{}
	})
	if n != nil {
		t.Fatalf("an unconfigured webhook must leave the Notifier nil (handle nil-checks it at transport.go:161), got %T", n)
	}
	if called {
		t.Error("ctor must not run when SLACK_WEBHOOK_URL is unset")
	}
}
