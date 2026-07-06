package contract_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestContractIsALeafPackage pins the Wave 1b invariant: internal/contract
// imports errors, time, and internal/proposal — NOTHING else. A clank, hiss,
// or thump import appearing here is an architecture regression, and this
// test is its tripwire.
func TestContractIsALeafPackage(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		`"errors"`: true,
		`"time"`:   true,
		`"github.com/ianeff/thump/api/v1/proposal"`: true,
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
				t.Errorf("%s imports %s — internal/contract must stay a leaf (errors/time + internal/proposal only)",
					name, imp.Path.Value)
			}
		}
	}
}
