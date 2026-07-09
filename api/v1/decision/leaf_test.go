package decision_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestDecisionIsALeafPackage pins that internal/decision stays a leaf: errors,
// fmt, time, and internal/proposal (Governed carries the Set) only. A hiss or
// thump import here is an architecture regression.
func TestDecisionIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "errors", "fmt", "time", "github.com/ianeff/thump/api/v1/proposal")
}
