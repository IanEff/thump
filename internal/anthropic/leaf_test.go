package anthropic_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestAnthropicIsALeafPackage pins that internal/anthropic stays a leaf:
// stdlib, the Anthropic SDK, and reason. It must never depend back on clank
// or a beat.
func TestAnthropicIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib,
		"github.com/anthropics/anthropic-sdk-go",
		"github.com/anthropics/anthropic-sdk-go/option",
		"github.com/ianeff/thump/internal/reason",
	)
}
