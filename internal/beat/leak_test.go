package beat_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running. Every
// long-running part of a beat — the shipper, the metrics server, a consumer —
// is a goroutine whose shutdown is a claim this package makes, and a leak here
// is that claim being wrong in a process meant to run for weeks.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
