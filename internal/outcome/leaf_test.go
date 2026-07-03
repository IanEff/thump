package outcome_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestOutcomeIsALeafPackage(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		`"errors"`: true, // Auditable's error values
		`"fmt"`:    true,
		`"time"`:   true,
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
				t.Errorf("%s imports %s — internal/outcome must stay a leaf (errors/fmt/time only)",
					name, imp.Path.Value)
			}
		}
	}
}
