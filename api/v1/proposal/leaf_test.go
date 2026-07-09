package proposal_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestProposalIsALeafPackage pins that internal/proposal stays a pure data
// leaf: time + internal/signal only.
func TestProposalIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "time", "github.com/ianeff/thump/api/v1/signal")
}
