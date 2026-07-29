package publish_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running — a
// Publisher (WALPublisher's segment rotation, JetPublisher's connection) that
// leaks a goroutine is a claim about its own shutdown being wrong.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
