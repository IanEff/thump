package tlsx_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestTlsxIsALeafPackage pins that internal/tlsx stays a leaf: it builds a
// *tls.Config from PEM files and nothing else, so no beat's internals leak
// into the one place a TLS config gets built. A new import here is an
// architecture regression.
func TestTlsxIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		"crypto/tls",
		"crypto/x509",
		"fmt",
		"os",
		"sync",
	)
}
