package clank_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ianeff/thump/internal/clank"
)

// TestCommittedCorpus_HoldsOnlyCasesTheCaseBaseWillAccept reads the shipped
// artifact rather than a hand-written fixture. Every other test in this
// package writes its cases in the shape the current structs expect, which is
// exactly the shape a rename changes — so a fixture can never catch a field
// that stopped round-tripping, and one did.
func TestCommittedCorpus_HoldsOnlyCasesTheCaseBaseWillAccept(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/corpus/corpus.json") //nolint:gosec // G304: fixed testdata path, not user input
	if err != nil {
		t.Fatal(err)
	}
	var got clank.Corpus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cases) == 0 {
		t.Fatal("committed corpus holds no cases — nothing to calibrate against")
	}

	base := clank.NewCaseBase()
	for _, c := range got.Cases {
		if err := base.Append(c); err != nil {
			t.Error("committed corpus holds a case its only consumer refuses", err)
		}
	}
}
