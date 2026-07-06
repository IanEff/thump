package publish_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestPublishIsALeafPackage pins Stage 1b's invariant: internal/publish is
// the one port every beat imports, and it imports none of them back — a
// rattle/clank/hiss/thump import creeping in here is the exact cycle this
// port exists to avoid.
func TestPublishIsALeafPackage(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		`"context"`:          true,
		`"fmt"`:              true,
		`"os"`:               true,
		`"path/filepath"`:    true,
		`"time"`:             true,
		`"sigs.k8s.io/yaml"`: true,
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
			if !allowed[imp.Path.Value] {
				t.Errorf("%s imports %s — internal/publish must stay a leaf (no rattle/clank/hiss/thump)",
					name, imp.Path.Value)
			}
		}
	}
}
