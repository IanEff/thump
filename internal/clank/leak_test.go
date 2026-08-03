package clank_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running — a leak
// in a beat is a claim about shutdown being wrong in a process meant to run
// for weeks.
//
// Two goroutines are ignored because they're not ours to stop. opencensus
// starts a stats worker from a package init() with no shutdown path, reached
// through model_gemini.go's genai import — it outlives every test. net/http
// keeps an HTTP/2 read loop alive per pooled connection for reuse; the
// eval-tagged suite's real Anthropic calls leave one running, blocked on a
// network read, so it surfaces as generic poll/runtime frames rather than
// its own name — matched anywhere in the stack instead of by top frame.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("go.opencensus.io/stats/view.(*worker).start"),
		goleak.IgnoreAnyFunction("net/http.(*http2ClientConn).readLoop"),
	)
}
