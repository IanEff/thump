package decision_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestDecisionIsALeafPackage pins the Wave 1a invariant: internal/decision
// imports errors, fmt, time, and internal/proposal — NOTHING else. A hiss
// or thump import appearing here is an architecture regression, and this
// test is its tripwire.
func TestDecisionIsALeafPackage(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		`"errors"`: true, // Auditable's error values
		`"fmt"`:    true,
		`"time"`:   true,
		`"github.com/ianeff/thump/internal/proposal"`: true, // Governed carries the Set
	}
	entries, err := os.ReadDir(".") // go test runs with the package dir as CWD
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
				t.Errorf("%s imports %s — internal/decision must stay a leaf (errors/fmt/time + internal/proposal only)",
					name, imp.Path.Value)
			}
		}
	}
}
