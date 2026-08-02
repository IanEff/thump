package health_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestHealthIsALeafPackage pins that internal/health stays a leaf: stdlib
// only. Every beat, plus beat.BrokerHooks and beat.AwaitConsumers, depends
// on this package; it must never depend back on one of them.
func TestHealthIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib)
}
