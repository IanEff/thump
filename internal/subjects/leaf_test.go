package subjects_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestSubjectsIsALeafPackage pins that internal/subjects stays a leaf: stdlib
// only. Both the evidence tools and the change source resolve subjects
// through this package; it must never depend back on one of them.
func TestSubjectsIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib)
}
