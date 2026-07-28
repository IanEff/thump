package thump_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine running — a leak
// in a beat is a claim about shutdown being wrong in a process meant to run
// for weeks.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
