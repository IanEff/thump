package contract_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestContractIsALeafPackage pins that internal/contract stays a leaf: errors,
// time, and internal/proposal only. A clank, hiss, or thump import here is an
// architecture regression.
func TestContractIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "errors", "time", "github.com/ianeff/thump/api/v1/proposal")
}
