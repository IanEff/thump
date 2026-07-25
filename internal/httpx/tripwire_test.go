package httpx_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyHttpxBuildsAnHTTPClient pins the bound where it can actually be
// enforced. http.DefaultClient's Timeout is zero, and every hand-rolled
// &http.Client{} is one more chance to leave the field off, so internal/httpx
// is the only non-test package allowed to name either. A hit anywhere else is
// an unbounded call to a backend that can stop a beat with no error and no
// metric.
func TestOnlyHttpxBuildsAnHTTPClient(t *testing.T) {
	t.Parallel()
	// Resolved to an absolute path so the root's own DirEntry.Name() is the
	// repo directory's name, never "..": filepath.Base("../..") is ".." and
	// would otherwise match the dot-dir skip below on the very first call,
	// SkipDir-ing the root before the walk visits anything.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	selfDir := filepath.Join(repoRoot, "internal", "httpx")
	fset := token.NewFileSet()

	err = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != repoRoot && (strings.HasPrefix(d.Name(), ".") || d.Name() == "bin") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Dir(path) == selfDir {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SelectorExpr:
				if isNetHTTP(v.X) && v.Sel.Name == "DefaultClient" {
					t.Errorf("%s:%d names http.DefaultClient, whose Timeout is zero — use httpx.Client",
						path, fset.Position(v.Pos()).Line)
				}
			case *ast.CompositeLit:
				sel, ok := v.Type.(*ast.SelectorExpr)
				if ok && isNetHTTP(sel.X) && sel.Sel.Name == "Client" {
					t.Errorf("%s:%d builds an http.Client literal — internal/httpx is the only package that may",
						path, fset.Position(v.Pos()).Line)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// isNetHTTP reports whether an expression is the net/http package qualifier.
// The one aliased http import in the tree is smithy-go's, spelled smithyhttp,
// so the bare identifier is the whole check.
func isNetHTTP(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "http"
}
