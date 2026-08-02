package poll_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestPollIsALeafPackage pins that internal/poll stays a leaf: stdlib only.
// Every beat's offline dir-poll transport depends on this package; it must
// never depend back on one of them.
func TestPollIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib)
}
