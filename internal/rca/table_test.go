package rca_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/rca"
	"github.com/ianeff/thump/internal/subjects"
)

// TestTable_GradesOnlyQueriesTheShippedEvidenceConfigDefines runs with no API
// key and no network. MustCite and MustNotCite are strings matched against
// Candidate.Citations, so a query renamed in evidence-queries.yaml turns a
// required citation into one that can never be satisfied — the row then reads
// as a reasoner regression on every run, and the fix gets applied to the
// reasoner. Evidence keys are checked against both key spaces it can name —
// see Case.Evidence's doc comment — so a typo in a loki subject name fails
// here instead of silently scripting a fake nobody's query ever reads.
func TestTable_GradesOnlyQueriesTheShippedEvidenceConfigDefines(t *testing.T) {
	t.Parallel()

	ev, err := evidence.LoadEvidenceConfig(
		filepath.Join("..", "..", "config", "thump-test", "whir", "evidence-queries.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	table := rca.Table()
	if len(table) == 0 {
		t.Fatal("the graded table is empty — there is nothing to instrument")
	}

	for _, tc := range table {
		t.Run("row "+tc.Name+" cites only defined queries and an existing fixture", func(t *testing.T) {
			t.Parallel()
			for _, q := range slices.Concat(tc.MustCite, tc.MustNotCite) {
				if _, ok := ev.Queries[q]; !ok {
					t.Error("row grades a query evidence-queries.yaml does not define", q)
				}
			}
			for q := range tc.Evidence {
				_, isQuery := ev.Queries[q]
				isSubject := slices.ContainsFunc(ev.Index, func(r subjects.SubjectRule) bool { return r.Subject == q })
				if !isQuery && !isSubject {
					t.Error("row scripts a value for a query or subject that does not exist", q)
				}
			}
			path := filepath.Join("..", "clank", "testdata", "detections", tc.Fixture)
			if _, err := os.Stat(path); err != nil {
				t.Error("row names a detection fixture that is not on disk", err)
			}
		})
	}
}
