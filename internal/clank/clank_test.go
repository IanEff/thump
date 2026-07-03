package clank_test

import (
	"bytes"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/clank"
)

func TestMain_VersionFlag(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	args := []string{"-version"}
	exitCode := clank.Main(args, stdout, stderr, "v1.0.0", "abcdef", "2026-06-29")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	want := "clank v1.0.0\ncommit: abcdef\nbuilt: 2026-06-29\n"
	if diff := cmp.Diff(want, stdout.String()); diff != "" {
		t.Error("wrong --version output (-want +got)\n", diff)
	}
}

// TODO(straggler A3): TestMain_TheEngineAndReturnEdgeShareOneLedgerAndCaseBase
// is parked until newTestLoop/writeOutcomeFor/unmatchedCount exist. Its own step,
// after Claim A1 (Transport) is green.
//
// func TestMain_TheEngineAndReturnEdgeShareOneLedgerAndCaseBase(t *testing.T) {
// 	t.Parallel()
// 	// build the loop the way Main does, then prove the two halves are wired to
// 	// the SAME state — a full round trip: propose (records a set) → hand-built
// 	// Outcome for that set → ReturnEdge observes it → the case is banked where
// 	// the scorer will find it next cycle.
// 	loop := newTestLoop(t) // constructs Engine + ReturnEdge via the SAME shared() helper Main uses
//
// 	det := seamDetection(t)
// 	set, err := loop.Engine.Propose(context.Background(), det) // records into the shared ledger
// 	if err != nil {
// 		t.Fatal(err)
// 	}
//
// 	// an Outcome answering that exact set, dropped in the return-edge inbox:
// 	writeOutcomeFor(t, loop.OutcomeInbox, set) // live success, DecisionRef/SignalRef threaded
// 	if err := loop.ReturnEdge.Tick(context.Background()); err != nil {
// 		t.Fatal(err)
// 	}
//
// 	// the outcome was MATCHED (not unmatched) — proof the ledgers are one:
// 	if n := unmatchedCount(t, loop.OutcomeInbox); n != 0 {
// 		t.Fatalf("the return edge saw an empty ledger — Main built TWO *MemProposalLog: %d unmatched", n)
// 	}
// 	// …and the case is where the scorer reads it — proof the case bases are one:
// 	if got := len(loop.Cases.Cases(det.Fingerprint)); got != 1 {
// 		t.Errorf("Absorb banked into a case base the scorer can't see: want 1, got %d", got)
// 	}
// }
