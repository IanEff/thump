package github_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestGitHubIsALeafPackage pins that internal/forge/github stays outside
// internal/actuate's import graph — the constraint that keeps client-go
// (actuate's dependency) from riding along behind an HTTP client.
func TestGitHubIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, leaftest.Stdlib, "github.com/ianeff/thump/internal/forge", "github.com/ianeff/thump/internal/httpx")
}
