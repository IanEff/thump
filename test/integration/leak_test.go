package integration_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running.
//
// The one ignored goroutine is not ours and cannot be stopped: opencensus
// starts a stats worker from a package init() and offers no shutdown, and it
// reaches here through internal/clank's genai import → cloud.google.com/go/auth.
// It exists before any test runs and outlives all of them; ignoring it by top
// function keeps the check strict about every goroutine this package starts.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("go.opencensus.io/stats/view.(*worker).start"),
	)
}
