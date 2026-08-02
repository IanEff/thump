package gemini_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestGeminiIsALeafPackage pins that internal/gemini stays a leaf: stdlib,
// the genai SDK, and reason. It must never depend back on clank or a beat.
func TestGeminiIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib,
		"google.golang.org/genai",
		"github.com/ianeff/thump/internal/reason",
	)
}
