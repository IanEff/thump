// Package configtest loads the shipped action config for tests through the
// same loaders the beats use in production — a hand-edit that would break a
// running clank cannot pass CI, because no test here decodes a config file a
// second way. It imports only internal/contract, so a leaf package's own
// tests can use it without dragging a beat into their test binary.
package configtest

import (
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/contract"
)

// shippedPath resolves a config/actions file from any test directory two
// levels below the repo root — internal/<pkg> and test/<pkg> both qualify.
func shippedPath(name string) string {
	return filepath.Join("..", "..", "config", "actions", name)
}

// ShippedCatalog loads the action catalog production runs on. An action
// absent from here can be neither proposed nor executed by anything.
func ShippedCatalog(t *testing.T) *contract.StaticCatalog {
	t.Helper()
	return CatalogAt(t, shippedPath("catalog.yaml"))
}

// ShippedFailureClasses loads the authored class definitions production
// renders into the reason loop's prompt.
func ShippedFailureClasses(t *testing.T) []contract.FailureClassDefinition {
	t.Helper()
	return FailureClassesAt(t, shippedPath("failure-classes.yaml"))
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
