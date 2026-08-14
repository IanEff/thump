package httpx_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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

// declaredPlaintext maps each non-test file allowed to run a leg in the clear
// to the reason it does. A row exempts WithInsecure and nothing else —
// InsecureSkipVerify is refused in every file, declared or not. Adding a row
// is a design review, not a convenience.
var declaredPlaintext = map[string]string{
	"internal/otelx/trace.go": "the http:// branch of OTEL_EXPORTER_OTLP_ENDPOINT is an authored operator choice for a rig whose collector doesn't serve TLS, not a delegation to Cilium WireGuard — the https:// branch verifies the peer via tlsx.Client",
}

// TestOnlyDeclaredFilesAskForAnInsecureTransport walks every non-test .go file
// in the tree and refuses any naming of an insecure-transport option that the
// declaredPlaintext allowlist doesn't cover. The allowlist reaches WithInsecure
// only: InsecureSkipVerify has no exception anywhere, because a plaintext leg
// can be authored with its reason on record and an unauthenticated TLS session
// dressed as a secure one cannot.
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
		_, declared := declaredPlaintext[filepath.ToSlash(rel)]
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, use := range insecureTransportNames(fset, f) {
			switch use.Name {
			case "WithInsecure":
				if declared {
					continue
				}
				t.Errorf("%s:%d names WithInsecure — a plaintext leg needs a row in declaredPlaintext carrying the reason",
					rel, use.Line)
			case "InsecureSkipVerify":
				t.Errorf("%s:%d sets InsecureSkipVerify — that is not a plaintext exception, it is an unauthenticated TLS session",
					rel, use.Line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// insecureUse is one syntactic naming of an insecure-transport option: the
// identifier and the line, never the value — naming the option at all is the
// violation, so a false is reported the same as a true.
type insecureUse struct {
	Name string // the option identifier as written, either WithInsecure or InsecureSkipVerify
	Line int    // 1-based line of the identifier, not of the statement enclosing it
}

// TestInsecureTransportNames_ReportsEverySyntaxThatCanNameAnInsecureOption is
// the table the real-tree walk can't be: the walk's input is the repo, so a
// red case there would mean committing a violating file. Every syntax that can
// name the option gets a row, because a guard that catches one spelling of a
// setting catches nothing.
func TestInsecureTransportNames_ReportsEverySyntaxThatCanNameAnInsecureOption(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src  string
		want []insecureUse
	}{
		"insecureTransportNames reports InsecureSkipVerify when it is written as a composite-literal field key": {
			src: `package p

import "crypto/tls"

var c = &tls.Config{InsecureSkipVerify: true}
`,
			want: []insecureUse{{Name: "InsecureSkipVerify", Line: 5}},
		},
		"insecureTransportNames reports InsecureSkipVerify when it is written as a field assignment": {
			src: `package p

import "crypto/tls"

func f(c *tls.Config) { c.InsecureSkipVerify = true }
`,
			want: []insecureUse{{Name: "InsecureSkipVerify", Line: 5}},
		},
		"insecureTransportNames reports InsecureSkipVerify set to false because naming the option is the violation, not the value": {
			src: `package p

import "crypto/tls"

var c = &tls.Config{InsecureSkipVerify: false}
`,
			want: []insecureUse{{Name: "InsecureSkipVerify", Line: 5}},
		},
		"insecureTransportNames reports WithInsecure when it is written as a call": {
			src: `package p

import "google.golang.org/grpc"

var o = grpc.WithInsecure()
`,
			want: []insecureUse{{Name: "WithInsecure", Line: 5}},
		},
		"insecureTransportNames reports both options when one file names each, in source order": {
			src: `package p

import (
	"crypto/tls"

	"google.golang.org/grpc"
)

var o = grpc.WithInsecure()
var c = &tls.Config{InsecureSkipVerify: true}
`,
			want: []insecureUse{
				{Name: "WithInsecure", Line: 9},
				{Name: "InsecureSkipVerify", Line: 10},
			},
		},
		"insecureTransportNames reports nothing for a file that builds a peer-verifying tls.Config": {
			src: `package p

import "crypto/tls"

var c = &tls.Config{MinVersion: tls.VersionTLS13}
`,
			want: nil,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", tc.src, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := insecureTransportNames(fset, f)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong insecure-transport findings", diff)
			}
		})
	}
}

// insecureTransportNames reports every identifier in f naming an option that
// disables transport authentication, whichever syntax spells it — a selector,
// a composite-literal field key, or a call. The value is never consulted:
// naming the option is what needs a row in declaredPlaintext.
func insecureTransportNames(fset *token.FileSet, f *ast.File) []insecureUse {
	var found []insecureUse
	record := func(id *ast.Ident) {
		switch id.Name {
		case "WithInsecure", "InsecureSkipVerify":
			found = append(found, insecureUse{Name: id.Name, Line: fset.Position(id.Pos()).Line})
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			record(x.Sel)
		case *ast.KeyValueExpr:
			if id, ok := x.Key.(*ast.Ident); ok {
				record(id)
			}
		}
		return true
	})
	return found
}
