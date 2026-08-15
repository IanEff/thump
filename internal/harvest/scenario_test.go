package harvest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/harvest"
	"github.com/ianeff/thump/internal/rattle"
)

// scenarioTables are every committed harvest table — read once per test so
// adding a third rig only means adding an entry here, never a third copy of
// each guard.
var scenarioTables = map[string]string{
	"thump-test": filepath.Join("..", "..", "chaos", "scenarios.yaml"),
	"dev":        filepath.Join("..", "..", "chaos", "scenarios-dev.yaml"),
}

// TestScenarios_NameOnlyVocabularyTheShippedCatalogAndClassesDefine reads the
// committed tables, not a fixture. Scenario.Expects.ContractRef is a string
// and so is every action name in each rig's catalog.yaml, so a typo'd or
// retired action compiles, loads, fires a real fault at a real cluster, and
// then waits out the whole settle window for a match that can never happen.
func TestScenarios_NameOnlyVocabularyTheShippedCatalogAndClassesDefine(t *testing.T) {
	t.Parallel()

	for rigLabel, path := range scenarioTables {
		t.Run(rigLabel, func(t *testing.T) {
			t.Parallel()

			table, err := harvest.LoadScenarios(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(table.Scenarios) == 0 {
				t.Fatal("the committed scenario table is empty — a harvest has nothing to fire")
			}

			cat, err := contract.LoadCatalogFile(
				filepath.Join("..", "..", "config", table.Rig, "actions", "catalog.yaml"), contract.Preconditions)
			if err != nil {
				t.Fatal(err)
			}

			for _, sc := range table.Scenarios {
				t.Run("scenario "+sc.Name+" names a catalogued action and an existing fault", func(t *testing.T) {
					t.Parallel()
					// A refused row expects no contract by construction — the
					// decoy's whole point is that no catalogued action applies.
					if sc.Expects.Verdict != "refused" {
						if _, ok := cat.ByName(sc.Expects.ContractRef); !ok {
							t.Error("scenario expects an action the catalog does not list", sc.Expects.ContractRef)
						}
					}
					if _, err := os.Stat(filepath.Join("..", "..", sc.Fault.Path)); err != nil {
						t.Error("scenario names a fault file that is not in the repo", err)
					}
					if sc.Restore.Path == "" {
						t.Error("scenario has no restore — a harvest that cannot put the rig back is a rig teardown")
					}
				})
			}
		})
	}
}

// TestScenarios_WaitOnFingerprintsTheRigsWatchListCanActuallyProduce reads the
// rig each table names and the watch list that rig polls. Settle matches on
// SignalRef alone, and both sides are strings, so a fingerprint no detector
// emits fires a real fault, waits out the full settle window, and returns a
// timeout indistinguishable from a rig that genuinely never settled.
func TestScenarios_WaitOnFingerprintsTheRigsWatchListCanActuallyProduce(t *testing.T) {
	t.Parallel()

	for rigLabel, path := range scenarioTables {
		t.Run(rigLabel, func(t *testing.T) {
			t.Parallel()

			table, err := harvest.LoadScenarios(path)
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
		})
	}
}

// TestScenarios_ExpectAFailureClassTheNamedActionIsActuallyScopedTo pins the
// join a Result's grade rests on: expects.contractRef and expects.failureClass
// are authored independently, and a row naming a class its own action does not
// list scores every run against a comparison that could never hold.
func TestScenarios_ExpectAFailureClassTheNamedActionIsActuallyScopedTo(t *testing.T) {
	t.Parallel()

	for rigLabel, path := range scenarioTables {
		t.Run(rigLabel, func(t *testing.T) {
			t.Parallel()

			table, err := harvest.LoadScenarios(path)
			if err != nil {
				t.Fatal(err)
			}

			cat, err := contract.LoadCatalogFile(
				filepath.Join("..", "..", "config", table.Rig, "actions", "catalog.yaml"), contract.Preconditions)
			if err != nil {
				t.Fatal(err)
			}

			for _, sc := range table.Scenarios {
				if sc.Expects.Verdict == "refused" {
					continue // no contract named — nothing to join
				}
				t.Run("scenario "+sc.Name+" names a failure class its own action is scoped to", func(t *testing.T) {
					t.Parallel()
					action, ok := cat.ByName(sc.Expects.ContractRef)
					if !ok {
						t.Fatal("scenario expects an action the catalog does not list", sc.Expects.ContractRef)
					}
					found := false
					for _, class := range action.ApplicableFailureClasses {
						if class == sc.Expects.FailureClass {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("scenario expects failureClass %q, but %q is only scoped to %v",
							sc.Expects.FailureClass, sc.Expects.ContractRef, action.ApplicableFailureClasses)
					}
				})
			}
		})
	}
}
