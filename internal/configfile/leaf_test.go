package configfile_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestConfigfileIsALeafPackage pins that internal/configfile stays a leaf:
// stdlib plus sigs.k8s.io/yaml only. Every staged-YAML loader in this repo
// depends on this package; it must never depend back on one of them.
func TestConfigfileIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t, "fmt", "os", "strings", "time", "sigs.k8s.io/yaml")
}
