package rca_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ianeff/thump/internal/contract"
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

// TestTable_GradesEveryRowAgainstTheRigWhoseCatalogActuallyDeclaresItsAction
// pins the split phase AT introduced: dev's catalog still ships three Ceph
// contracts whose success metrics exist in no dev evidence query, so a Ceph
// row graded under -rig dev resolves a contract it can never verify and
// scores a pass on a metric the profile cannot answer.
func TestTable_GradesEveryRowAgainstTheRigWhoseCatalogActuallyDeclaresItsAction(t *testing.T) {
	t.Parallel()

	cats := make(map[string]*contract.StaticCatalog)
	for _, c := range rca.Table() {
		if c.Rig == "" {
			t.Errorf("row %q names no Rig — it would be silently skipped under every -rig flag", c.Name)
			continue
		}
		if _, ok := cats[c.Rig]; !ok {
			cat, err := contract.LoadCatalogFile(
				filepath.Join("..", "..", "config", c.Rig, "actions", "catalog.yaml"), contract.Preconditions)
			if err != nil {
				t.Fatalf("row %q names rig %q whose catalog does not load: %v", c.Name, c.Rig, err)
			}
			cats[c.Rig] = cat
		}
		if c.WantContractRef == "" {
			continue
		}
		if _, ok := cats[c.Rig].ByName(c.WantContractRef); !ok {
			t.Errorf("row %q wants contract %q, which %s's catalog does not declare", c.Name, c.WantContractRef, c.Rig)
		}
	}
}
