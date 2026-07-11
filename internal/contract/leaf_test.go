package contract_test

import (
	"testing"

	"github.com/ianeff/thump/internal/leaftest"
)

// TestContractIsALeafPackage pins that internal/contract stays a leaf:
// errors, fmt, os, time, sigs.k8s.io/yaml (load.go's file/YAML loader), and
// internal/proposal only. A clank, hiss, or thump import here is an
// architecture regression.
func TestContractIsALeafPackage(t *testing.T) {
	t.Parallel()
	leaftest.AssertLeaf(t,
		"errors",
		"fmt",
		"os",
		"time",
		"sigs.k8s.io/yaml",
		"github.com/ianeff/thump/api/v1/proposal",
	)
}
