package schema_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestSchemaIsALeafPackage pins that internal/schema stays a leaf: stdlib
// plus the jsonschema reflector it wraps. Every tool spec builder across the
// beats depends on this package; it must never depend back on one of them.
func TestSchemaIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib, "github.com/invopop/jsonschema")
}
