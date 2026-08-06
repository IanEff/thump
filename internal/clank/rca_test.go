//go:build eval

package clank_test

import (
	"os"
	"testing"

	"github.com/ianeff/thump/internal/rca"
)

// TestRCA drives the graded suite through its real entry point, so the
// eval-tagged route and the CLI route cannot diverge. Args are nil, never
// os.Args[1:] — the test binary's own flags would otherwise reach rca's
// FlagSet and fail the parse.
func TestRCA(t *testing.T) {
	if code := rca.Main(nil, os.Stdout, os.Stderr); code != 0 {
		t.Errorf("graded suite did not clear its floor (exit %d)", code)
	}
}
