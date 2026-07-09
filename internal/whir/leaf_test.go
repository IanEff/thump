package whir_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestWhirIsALeafPackage pins that internal/whir stays a leaf: stdlib HTTP/JSON
// plumbing and sigs.k8s.io/yaml only — no beat internals.
func TestWhirIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		"bytes", "context", "encoding/json", "fmt", "net/http", "net/url",
		"os", "strconv", "strings", "sigs.k8s.io/yaml",
	)
}
