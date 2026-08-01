package publish_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/publish"
)

// walConfigBeforeExtraction is the WAL sealing and shipping cadence every
// beat had hardcoded before X2a, hand-transcribed and never regenerated —
// the fixed point the extraction is measured against.
func walConfigBeforeExtraction() publish.WALConfig {
	return publish.WALConfig{
		MaxBytes:     64 * 1024 * 1024,
		MaxAge:       10 * time.Minute,
		SyncInterval: 5 * time.Second,
		ShipInterval: 30 * time.Second,
	}
}

func TestLoadWALConfig_TheShippedDefaultsEqualTheConstantsTheyReplaced(t *testing.T) {
	t.Parallel()
	// Extraction is a refactor, not a tuning change — the shipped default
	// equals the constant it replaced.
	got, err := publish.LoadWALConfig(filepath.Join("..", "..", "config", "wal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(walConfigBeforeExtraction(), got); diff != "" {
		t.Error("shipped wal config drifted from the constants it replaced (-want +got)\n", diff)
	}
}

func TestLoadWALConfig_RejectsAPartialFileRatherThanZeroingTheMissingTerms(t *testing.T) {
	t.Parallel()
	// A zero WALConfig doesn't degrade gracefully — ShipInterval: 0 would
	// spin RunShipper's poll loop with no wait at all. Config in this tree
	// fails at load, never at first use.
	partial := map[string]string{
		"one term omitted":  "maxBytes: 67108864\nmaxAge: 10m\n",
		"every term absent": "\n",
	}
	for name, body := range partial {
		t.Run("LoadWALConfig rejects a file with "+name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "wal.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := publish.LoadWALConfig(path)
			if !errors.Is(err, publish.ErrIncompleteWALConfig) {
				t.Errorf("want ErrIncompleteWALConfig, got %v — a missing term silently becomes zero", err)
			}
		})
	}
}
