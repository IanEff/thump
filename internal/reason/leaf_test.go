package reason_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestReasonIsALeafPackage pins that internal/reason stays a leaf: stdlib
// plus the two wire vocabularies its seams cross. Every adapter clank plugs
// into the reason loop — a Model, a Tool, a TopologySource, a ChangeSource —
// depends on this package; it must never depend back on clank or any beat.
func TestReasonIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib,
		"github.com/ianeff/thump/api/v1/proposal",
		"github.com/ianeff/thump/api/v1/signal",
	)
}
