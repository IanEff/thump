// leaf_test.go
package whir_test

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestWhirIsALeafPackage(t *testing.T) {
	t.Parallel()
	allowed := map[string]bool{
		`"bytes"`:            true,
		`"context"`:          true,
		`"encoding/json"`:    true,
		`"fmt"`:              true,
		`"net/http"`:         true,
		`"net/url"`:          true,
		`"os"`:               true,
		`"strconv"`:          true,
		`"strings"`:          true,
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
				t.Errorf("%s imports %s — internal/whir must stay a leaf", name, imp.Path.Value)
			}
		}
	}
}
