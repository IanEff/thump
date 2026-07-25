// Package configtest loads the shipped action config for tests through the
// same loaders the beats use in production — a hand-edit that would break a
// running clank cannot pass CI, because no test here decodes a config file a
// second way. It imports only internal/contract, so a leaf package's own
// tests can use it without dragging a beat into their test binary.
package configtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/contract"
)

// shippedPath resolves a config/actions file against the repo root, located by
// walking up from the test's working directory to the enclosing go.mod. How
// deep the calling package sits is therefore irrelevant — a test nested below
// internal/<pkg> reads the same shipped config as one beside it, instead of a
// relative path that silently resolves outside the repo.
func shippedPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "config", "actions", name)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s: cannot locate the repo root holding config/actions/%s", dir, name)
		}
		dir = parent
	}
}

// ShippedCatalog loads the action catalog production runs on. An action
// absent from here can be neither proposed nor executed by anything.
func ShippedCatalog(t *testing.T) *contract.StaticCatalog {
	t.Helper()
	return CatalogAt(t, shippedPath(t, "catalog.yaml"))
}

// ShippedFailureClasses loads the authored class definitions production
// renders into the reason loop's prompt.
func ShippedFailureClasses(t *testing.T) []contract.FailureClassDefinition {
	t.Helper()
	return FailureClassesAt(t, shippedPath(t, "failure-classes.yaml"))
}

// CatalogAt loads an authored catalog from an arbitrary path — the seam a
// fixture domain's own catalog.yaml comes through, so a synthetic domain is
// loaded exactly as the shipped one is.
func CatalogAt(t *testing.T, path string) *contract.StaticCatalog {
	t.Helper()
	cat, err := contract.LoadCatalogFile(path, contract.Preconditions)
	if err != nil {
		t.Fatalf("load catalog %s: %v", path, err)
	}
	return cat
}

// FailureClassesAt loads authored class definitions from an arbitrary path.
func FailureClassesAt(t *testing.T, path string) []contract.FailureClassDefinition {
	t.Helper()
	defs, err := contract.LoadFailureClassesFile(path)
	if err != nil {
		t.Fatalf("load failure classes %s: %v", path, err)
	}
	return defs
}
