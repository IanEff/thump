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
	"github.com/ianeff/thump/internal/evidence"
)

// repoRootFrom walks up from dir to the enclosing go.mod — the same climb
// profilePath does, factored out so a caller outside config/<profile>/actions/
// (e.g. config/<profile>/whir/) can reuse it.
func repoRootFrom(t *testing.T, dir string) string {
	t.Helper()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s: cannot locate repo root", dir)
		}
		dir = parent
	}
}

// EvidenceQueriesForProfile loads the authored PromQL a profile's metrics
// tool exposes — the same file MetricsTool reads in production, so a test
// checking a catalog's SuccessCriteria against real query text can't drift
// from what clank and thump actually query.
func EvidenceQueriesForProfile(t *testing.T, profile string) evidence.Config {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	root := repoRootFrom(t, wd)
	cfg, err := evidence.LoadEvidenceConfig(filepath.Join(root, "config", profile, "whir", "evidence-queries.yaml"))
	if err != nil {
		t.Fatalf("load evidence queries for profile %s: %v", profile, err)
	}
	return cfg
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

// profilePath resolves a file in config/<profile>/actions/ against the repo
// root, located by walking up from the test's working directory to the
// enclosing go.mod. How deep the calling package sits is therefore
// irrelevant — a test nested below internal/<pkg> reads the same profile
// config as one beside it, instead of a relative path that silently
// resolves outside the repo.
func profilePath(t *testing.T, profile, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "config", profile, "actions", name)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s: cannot locate repo root holding config/%s/actions/%s", dir, profile, name)
		}
		dir = parent
	}
}

// CatalogForProfile loads an authored catalog for a specific cluster profile.
func CatalogForProfile(t *testing.T, profile string) *contract.StaticCatalog {
	t.Helper()
	return CatalogAt(t, profilePath(t, profile, "catalog.yaml"))
}

// FailureClassesForProfile loads authored failure classes for a specific profile.
func FailureClassesForProfile(t *testing.T, profile string) []contract.FailureClassDefinition {
	t.Helper()
	return FailureClassesAt(t, profilePath(t, profile, "failure-classes.yaml"))
}

// ShippedCatalog defaults to the full thump-test catalog so existing tests assert against complete contract coverage.
func ShippedCatalog(t *testing.T) *contract.StaticCatalog {
	t.Helper()
	return CatalogForProfile(t, "thump-test")
}

// ShippedFailureClasses defaults to thump-test failure classes.
func ShippedFailureClasses(t *testing.T) []contract.FailureClassDefinition {
	t.Helper()
	return FailureClassesForProfile(t, "thump-test")
}

// EvidenceQueriesAt loads authored evidence queries from an arbitrary path —
// the seam a fixture domain's own evidence-queries.yaml comes through.
func EvidenceQueriesAt(t *testing.T, path string) evidence.Config {
	t.Helper()
	cfg, err := evidence.LoadEvidenceConfig(path)
	if err != nil {
		t.Fatalf("load evidence queries %s: %v", path, err)
	}
	return cfg
}

// evidencePath resolves a file in config/<profile>/whir/ against the repo
// root, located by walking up from the test's working directory to the
// enclosing go.mod.
func evidencePath(t *testing.T, profile, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "config", profile, "whir", name)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s: cannot locate repo root holding config/%s/whir/%s", dir, profile, name)
		}
		dir = parent
	}
}

// EvidenceQueries loads authored evidence queries for a specific cluster profile.
func EvidenceQueries(t *testing.T, profile string) evidence.Config {
	t.Helper()
	return EvidenceQueriesAt(t, evidencePath(t, profile, "evidence-queries.yaml"))
}
