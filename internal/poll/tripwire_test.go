package poll_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/poll"
)

// TestAllPollLoopCallsitesBoundTheirTick is Loop's sibling to
// TestOnlyHttpxBuildsAnHTTPClient: Config.Timeout's doc comment says every
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
			if isDefaultConfigRef(call.Args[1]) {
				// TestDefaultConfigHasANonZeroTimeout proves this one's
				// Timeout directly; no source-level re-derivation needed here.
				return true
			}
			cfg, ok := call.Args[1].(*ast.CompositeLit)
			if !ok {
				t.Errorf("%s:%d builds Config from something other than a literal — this tripwire can't verify Timeout is set here",
					path, fset.Position(call.Pos()).Line)
				return true
			}
			if !hasNonZeroTimeout(cfg) {
				t.Errorf("%s:%d calls Loop with no non-zero Timeout — the tick is unbounded",
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

// TestDefaultConfigHasANonZeroTimeout is what actually backs
// isDefaultConfigRef's trust: a call site that passes poll.DefaultConfig is
// exempted from the literal-Timeout check above only because this test
// independently proves the shared value itself is bounded.
func TestDefaultConfigHasANonZeroTimeout(t *testing.T) {
	t.Parallel()
	if poll.DefaultConfig.Timeout == 0 {
		t.Error("DefaultConfig.Timeout is 0 — every Loop call site sharing it would tick unbounded")
	}
}

// isDefaultConfigRef reports whether expr references poll.DefaultConfig —
// qualified (every beat's call sites) or bare (Loop's own package, matching
// isPollLoopCall's symmetry).
func isDefaultConfigRef(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name == "DefaultConfig"
	case *ast.SelectorExpr:
		id, ok := v.X.(*ast.Ident)
		return ok && id.Name == "poll" && v.Sel.Name == "DefaultConfig"
	}
	return false
}

// isPollLoopCall matches both the qualified call every beat makes
// (poll.Loop) and the unqualified one Loop's own package would use.
func isPollLoopCall(fun ast.Expr) bool {
	switch v := fun.(type) {
	case *ast.Ident:
		return v.Name == "Loop"
	case *ast.SelectorExpr:
		id, ok := v.X.(*ast.Ident)
		return ok && id.Name == "poll" && v.Sel.Name == "Loop"
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
