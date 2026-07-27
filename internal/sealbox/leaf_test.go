package sealbox_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestSealboxIsALeafPackage pins that internal/sealbox stays a leaf: it seals
// and opens bytes with AES-256-GCM and nothing else, so no beat's internals
// leak into the one place ciphertext gets produced. A new import here is an
// architecture regression.
func TestSealboxIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		"crypto/aes",
		"crypto/cipher",
		"crypto/rand",
		"fmt",
	)
}
