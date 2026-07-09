// Package leaftest is the shared body of every leaf package's import tripwire.
// A leaf package (the api/v1 wire contracts, the authored catalog, the shared
// ports) must import only a small, declared set of packages, so that no beat's
// internals leak across a boundary the whole design rests on. Each leaf's
// leaf_test.go calls AssertLeaf with exactly what it is allowed to import; a
// new import that isn't on the list fails the test as an architecture
// regression.
package leaftest

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// AssertLeaf fails t if any non-test .go file in the current directory (the
// package under test) imports a path not in allowed. Pass import paths
// unquoted, e.g. AssertLeaf(t, "time", "github.com/ianeff/thump/api/v1/signal").
func AssertLeaf(t *testing.T, allowed ...string) {
	t.Helper()
	permitted := make(map[string]bool, len(allowed))
	for _, p := range allowed {
		permitted[p] = true
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !permitted[path] {
				t.Errorf("%s imports %q — this package must stay a leaf; allowed imports are %v", name, path, allowed)
			}
		}
	}
}
