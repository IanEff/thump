package harvest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/harvest"
	"github.com/ianeff/thump/internal/rattle"
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
	if len(table.Scenarios) == 0 {
		t.Fatal("the committed scenario table is empty — a harvest has nothing to fire")
	}

	cat, err := contract.LoadCatalogFile(
		filepath.Join("..", "..", "config", "thump-test", "actions", "catalog.yaml"), contract.Preconditions)
	if err != nil {
		t.Fatal(err)
	}

	for _, sc := range table.Scenarios {
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

// TestScenarios_WaitOnFingerprintsTheRigsWatchListCanActuallyProduce reads the
// rig the table names and the watch list that rig polls. Settle matches on
// SignalRef alone, and both sides are strings, so a fingerprint no detector
// emits fires a real fault, waits out the full settle window, and returns a
// timeout indistinguishable from a rig that genuinely never settled.
func TestScenarios_WaitOnFingerprintsTheRigsWatchListCanActuallyProduce(t *testing.T) {
	t.Parallel()

	table, err := harvest.LoadScenarios(filepath.Join("..", "..", "chaos", "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	slos, err := rattle.LoadWatch(
		filepath.Join("..", "..", "config", table.Rig, "rattle", "watch.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	emittable := make(map[string]bool, len(slos))
	for _, s := range slos {
		emittable[s.Kind()+":"+s.AffectedObject()] = true
	}

	for _, sc := range table.Scenarios {
		t.Run("scenario "+sc.Name+" waits on a fingerprint the watch list emits", func(t *testing.T) {
			t.Parallel()
			if !emittable[sc.SignalRef] {
				t.Error("scenario waits on a fingerprint no watched object produces", sc.SignalRef)
			}
		})
	}
}
