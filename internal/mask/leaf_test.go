package mask_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestMaskIsALeafPackage pins that internal/mask stays a leaf: stdlib plus
// the one seam it wraps. Every reason-loop Model gets wrapped through this
// package before a real identifier can reach a provider; it must never
// depend back on clank or a beat.
func TestMaskIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib, "github.com/ianeff/thump/internal/reason")
}
