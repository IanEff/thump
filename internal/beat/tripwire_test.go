package beat_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestAllPollLoopCallsitesBoundTheirTick is PollLoop's sibling to
// TestOnlyHttpxBuildsAnHTTPClient: PollConfig.Timeout's doc comment says every
// call site must choose a number, but nothing enforced it. WithTimeout(0,
// tick) returns tick unchanged with no log or metric, so a call site that
// omits Timeout — or a future one that copies an existing site and forgets it
// — silently re-introduces the unbounded tick this PR exists to close.
func TestAllPollLoopCallsitesBoundTheirTick(t *testing.T) {
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
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isPollLoopCall(call.Fun) || len(call.Args) < 2 {
				return true
			}
			cfg, ok := call.Args[1].(*ast.CompositeLit)
			if !ok {
				t.Errorf("%s:%d builds PollConfig from something other than a literal — this tripwire can't verify Timeout is set here",
					path, fset.Position(call.Pos()).Line)
				return true
			}
			if !hasNonZeroTimeout(cfg) {
				t.Errorf("%s:%d calls PollLoop with no non-zero Timeout — the tick is unbounded",
					path, fset.Position(call.Pos()).Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// isPollLoopCall matches both the qualified call every other package makes
// (beat.PollLoop) and the unqualified one PollLoop's own package uses
// (objectstore.go).
func isPollLoopCall(fun ast.Expr) bool {
	switch v := fun.(type) {
	case *ast.Ident:
		return v.Name == "PollLoop"
	case *ast.SelectorExpr:
		id, ok := v.X.(*ast.Ident)
		return ok && id.Name == "beat" && v.Sel.Name == "PollLoop"
	}
	return false
}

// hasNonZeroTimeout reports whether cfg's Timeout field is present and not
// the literal 0. A non-literal value (the common case: `20 * time.Second`, or
// a named constant) is trusted as a deliberate choice — this tripwire catches
// an omitted field or a bare 0, not every possible expression that evaluates
// to zero.
func hasNonZeroTimeout(cfg *ast.CompositeLit) bool {
	for _, elt := range cfg.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Timeout" {
			continue
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		return !ok || lit.Value != "0"
	}
	return false
}
