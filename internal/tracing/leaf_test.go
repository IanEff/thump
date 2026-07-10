package tracing_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestTracingIsALeafPackage pins that internal/tracing stays a leaf:
// crypto/sha256 and the OTel trace API only — no beat internals. Every beat
// mints IDs off this package; it must never import one back.
func TestTracingIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "crypto/sha256", "go.opentelemetry.io/otel/trace")
}
