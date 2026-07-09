package outcome_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestOutcomeIsALeafPackage pins that internal/outcome stays a leaf:
// errors, fmt, time only.
func TestOutcomeIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "errors", "fmt", "time")
}
