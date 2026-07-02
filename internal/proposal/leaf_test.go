package proposal_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestProposalIsALeafPackage(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		`"time"`: true,
		`"github.com/ianeff/clank/internal/signal"`: true,
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
				t.Errorf("%s imports %s - internal/proposal must stay a leaf (time + internal/signal only)", name, imp.Path.Value)
			}
		}

	}
}
