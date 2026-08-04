package harvest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/harvest"
)

// TestScenarios_NameOnlyVocabularyTheShippedCatalogAndClassesDefine reads the
// committed table, not a fixture. Scenario.Expects.ContractRef is a string and
// so is every action name in catalog.yaml, so a typo'd or retired action
// compiles, loads, fires a real fault at a real cluster, and then waits out
// the whole settle window for a match that can never happen.
func TestScenarios_NameOnlyVocabularyTheShippedCatalogAndClassesDefine(t *testing.T) {
	t.Parallel()

	table, err := harvest.LoadScenarios(filepath.Join("..", "..", "chaos", "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(table) == 0 {
		t.Fatal("the committed scenario table is empty — a harvest has nothing to fire")
	}

	cat, err := contract.LoadCatalogFile(
		filepath.Join("..", "..", "config", "actions", "catalog.yaml"), contract.Preconditions)
	if err != nil {
		t.Fatal(err)
	}

	for _, sc := range table {
		t.Run("scenario "+sc.Name+" names a catalogued action and an existing fault", func(t *testing.T) {
			t.Parallel()
			if _, ok := cat.ByName(sc.Expects.ContractRef); !ok {
				t.Error("scenario expects an action the catalog does not list", sc.Expects.ContractRef)
			}
			if _, err := os.Stat(filepath.Join("..", "..", sc.Fault.Path)); err != nil {
				t.Error("scenario names a fault file that is not in the repo", err)
			}
			if sc.Restore.Path == "" {
				t.Error("scenario has no restore — a harvest that cannot put the rig back is a rig teardown")
			}
		})
	}
}
