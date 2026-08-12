package forge_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestForgeIsALeafPackage pins that internal/forge stays a leaf: stdlib only.
// It is the vocabulary the actuator and its GitHub client share, and either
// one importing the other through it would put client-go behind an HTTP
// client.
func TestForgeIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib)
}
