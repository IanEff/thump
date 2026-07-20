package approval_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestApprovalIsALeafPackage pins that api/v1/approval stays a leaf: errors and
// time only. A hiss or trim import here is an architecture regression — approval
// is meant to be importable by both without either importing the other.
func TestApprovalIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "errors", "time")
}
