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

// declaredTLSBuilders maps each non-test file allowed to construct a
// *tls.Config to why it isn't going through internal/tlsx. Empty is the
// correct steady state; a row is a design review.
var declaredTLSBuilders = map[string]string{}

// TestOnlyTlsxBuildsATLSConfig draws the same bound for *tls.Config that
// TestOnlyHttpxBuildsAnHTTPClient draws for *http.Client: a config assembled
// at the call site is a chance to leave MinVersion, RootCAs, or ClientAuth
// at its zero value, and every one of those mistakes succeeds at runtime
// instead of failing. A hit outside internal/tlsx is a leg nobody ran
// through tlsx.Client or tlsx.Server.
func TestOnlyTlsxBuildsATLSConfig(t *testing.T) {
	t.Parallel()
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tlsxDir := filepath.Join(repoRoot, "internal", "tlsx")
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
		if filepath.Dir(path) == tlsxDir {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		if _, declared := declaredTLSBuilders[filepath.ToSlash(rel)]; declared {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if ok && isCryptoTLS(sel.X) && sel.Sel.Name == "Config" {
				t.Errorf("%s:%d builds a *tls.Config literal — internal/tlsx is the only package that may; add a row to declaredTLSBuilders or route it through tlsx.Client/tlsx.Server",
					rel, fset.Position(lit.Pos()).Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// isCryptoTLS reports whether an expression is the crypto/tls package
// qualifier.
func isCryptoTLS(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "tls"
}

// declaredPlaintext maps each non-test file allowed to name an
// insecure-transport option to the reason that leg runs in the clear. A hit
// anywhere else is a leg nobody decided about; adding a row is a design
// review, not a convenience.
var declaredPlaintext = map[string]string{
	"internal/otelx/trace.go": "the http:// branch of OTEL_EXPORTER_OTLP_ENDPOINT is an authored operator choice for a rig whose collector doesn't serve TLS, not a delegation to Cilium WireGuard — the https:// branch verifies the peer via tlsx.Client",
}

func TestOnlyDeclaredFilesAskForAnInsecureTransport(t *testing.T) {
	t.Parallel()
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
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
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		if _, declared := declaredPlaintext[filepath.ToSlash(rel)]; declared {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "WithInsecure":
				t.Errorf("%s:%d names WithInsecure — a plaintext leg needs a row in declaredPlaintext carrying the reason",
					rel, fset.Position(sel.Pos()).Line)
			case "InsecureSkipVerify":
				t.Errorf("%s:%d sets InsecureSkipVerify — that is not a plaintext exception, it is an unauthenticated TLS session",
					rel, fset.Position(sel.Pos()).Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
