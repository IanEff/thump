package clank_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRunBroker_WrapsTheDetectionsStoreInHeartbeatingStore is X2c's fourth
// test — not a fourth timeout number, but the guard the other three depend
// on. TestModelRequestTimeout_ExceedsAckWaitAndIsCoveredByTheSubscriberHeartbeat
// (timeout_test.go) argues 120s is safe only because Handler's heartbeat
// resets AckWait on checkpoint progress, and that argument only holds if
// runBroker actually wires the heartbeat-decorated Store into the engine
// that serves thump.detections.
//
// NewBrokerEngineForTest (export_test.go:50) hands newBrokerEngine a bare
// Store on purpose — most tests exercising the engine don't want heartbeat
// plumbing — so no runtime test goes through the wrapped path via the real
// constructor. Deleting the wrap at broker.go:72 would leave the rest of the
// suite green: the dead-wiring pattern, with the wiring currently correct.
// This reuses X0a's own technique rather than inventing a new one: read the
// composition root's source with go/ast and require the wrap literally,
// since no runtime harness reaches this line without standing up a live
// NATS and S3 sink for a single assertion.
func TestRunBroker_WrapsTheDetectionsStoreInHeartbeatingStore(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "broker.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "newBrokerEngine" {
			return true
		}
		found = true

		const storeArgIndex = 2 // newBrokerEngine(model, intake, store, ...)
		if len(call.Args) <= storeArgIndex {
			t.Fatalf("newBrokerEngine call has %d args, want more than %d — the store argument moved and this test needs updating", len(call.Args), storeArgIndex)
			return false
		}

		lit, ok := call.Args[storeArgIndex].(*ast.CompositeLit)
		if !ok {
			t.Errorf("runBroker passes a %T as the Store argument to newBrokerEngine, want a HeartbeatingStore{...} composite literal — modelRequestTimeout's 120s bound is only safe because this decorator resets AckWait on checkpoint progress", call.Args[storeArgIndex])
			return false
		}
		if typeIdent, ok := lit.Type.(*ast.Ident); !ok || typeIdent.Name != "HeartbeatingStore" {
			t.Errorf("runBroker wraps the Store in %#v, want HeartbeatingStore{...} — the detections handler's heartbeat, and the coherence test built on it, depend on exactly this decorator", lit.Type)
		}
		return false
	})
	if !found {
		t.Fatal("no call to newBrokerEngine found in broker.go — runBroker's composition moved, and this test no longer looks at the right place")
	}
}
