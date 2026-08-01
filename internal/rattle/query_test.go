package rattle_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
)

// queryConfigBeforeExtraction is PromSource's step/window as they stood
// hardcoded in NewPromSource before X2a, hand-transcribed and never
// regenerated — the fixed point the extraction is measured against.
func queryConfigBeforeExtraction() rattle.QueryConfig {
	return rattle.QueryConfig{
		Step:   time.Minute,
		Window: 15 * time.Minute,
	}
}

// TestLoadQueryConfig_TheShippedDefaultsEqualTheConstantsTheyReplaced pins
// every rig's query.yaml — extraction is a refactor, not a tuning change,
// so every rig ships the same step/window it had hardcoded.
func TestLoadQueryConfig_TheShippedDefaultsEqualTheConstantsTheyReplaced(t *testing.T) {
	t.Parallel()
	for _, rig := range []string{"rook-gce-k3s", "rook-gke", "ceph-lab", "thump-test"} {
		t.Run(rig, func(t *testing.T) {
			t.Parallel()
			got, err := rattle.LoadQueryConfig(filepath.Join("..", "..", "config", rig, "rattle", "query.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(queryConfigBeforeExtraction(), got); diff != "" {
				t.Error("shipped query config drifted from the constants it replaced (-want +got)\n", diff)
			}
		})
	}
}

func TestLoadQueryConfig_RejectsAPartialFileRatherThanZeroingTheMissingTerms(t *testing.T) {
	t.Parallel()
	// A zero QueryConfig doesn't degrade gracefully — BurnSamples' own
	// zero-fallback would silently substitute its built-in default,
	// masking a config file that shipped broken. Config in this tree fails
	// at load, never at first use.
	partial := map[string]string{
		"one term omitted":  "step: 1m\n",
		"every term absent": "\n",
	}
	for name, body := range partial {
		t.Run("LoadQueryConfig rejects a file with "+name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "query.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := rattle.LoadQueryConfig(path)
			if !errors.Is(err, rattle.ErrIncompleteQueryConfig) {
				t.Errorf("want ErrIncompleteQueryConfig, got %v — a missing term silently becomes zero", err)
			}
		})
	}
}
