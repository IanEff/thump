package clank_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
)

// limitsBeforeExtraction is clank's case base, ledger, change lookback, and
// retry-budget constants as they stood before X2a, hand-transcribed and
// never regenerated — the fixed point the extraction is measured against.
// It must not be replaced by a call to LoadLimitsFile: a test comparing the
// loader to itself proves only that the file equals itself.
func limitsBeforeExtraction() clank.Limits {
	return clank.Limits{
		MaxCases:           10000,
		LedgerRetention:    24 * time.Hour,
		ChangeLookback:     2 * time.Hour,
		MaxProposeAttempts: 5,
		MaxSteps:           8,
	}
}

func TestLoadLimits_TheShippedDefaultsEqualTheConstantsTheyReplaced(t *testing.T) {
	t.Parallel()
	// Extraction is a refactor, not a tuning change — the shipped default
	// equals the constant it replaced.
	got, err := clank.LoadLimitsFile(filepath.Join("..", "..", "config", "clank", "limits.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(limitsBeforeExtraction(), got); diff != "" {
		t.Error("shipped limits config drifted from the constants it replaced (-want +got)\n", diff)
	}
}

func TestLoadLimits_RejectsAPartialFileRatherThanZeroingTheMissingTerms(t *testing.T) {
	t.Parallel()
	// A zero Limits does not degrade gracefully — MaxCases: 0 would evict
	// every case the moment it's appended, and MaxProposeAttempts: 0 would
	// stall a detection on its first failure. Config in this tree fails at
	// load, never at first use.
	partial := map[string]string{
		"one term omitted":  "maxCases: 10000\nledgerRetention: 24h\n",
		"every term absent": "\n",
	}
	for name, body := range partial {
		t.Run("LoadLimits rejects a file with "+name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "limits.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := clank.LoadLimitsFile(path)
			if !errors.Is(err, clank.ErrIncompleteLimits) {
				t.Errorf("want ErrIncompleteLimits, got %v — a missing term silently becomes zero", err)
			}
		})
	}
}
