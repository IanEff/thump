package clank_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
	"sigs.k8s.io/yaml"
)

// TestEngine_CarriesNoFloorPolicy is I-3 as an executable invariant: the
// confidence-floor vocabulary belongs to hiss (Policy.Floors), not clank. If a
// future refactor re-adds a threshold/floor field to clank's Engine (directly,
// or via ScoringWeights), this goes red.
func TestEngine_CarriesNoFloorPolicy(t *testing.T) {
	t.Parallel()
	for _, typ := range []reflect.Type{reflect.TypeOf(clank.Engine{}), reflect.TypeOf(clank.ScoringWeights{})} {
		for _, f := range reflect.VisibleFields(typ) {
			if strings.Contains(strings.ToLower(f.Name), "threshold") ||
				strings.Contains(strings.ToLower(f.Name), "floor") {
				t.Errorf("%s.%s is policy — it belongs in hiss.Policy (I-3)", typ.Name(), f.Name)
			}
		}
	}
}

func TestDefaultScoringWeights_LeavesNoFieldAtZero(t *testing.T) {
	t.Parallel()

	// A ScoringWeights field left at zero doesn't degrade gracefully — it
	// multiplies a whole scoring term out of existence. Walking the fields
	// by reflection means a field added to the struct but forgotten in the
	// defaults fails here instead of shipping as a dead term.
	v := reflect.ValueOf(clank.DefaultScoringWeights())
	for _, f := range reflect.VisibleFields(v.Type()) {
		if v.FieldByIndex(f.Index).IsZero() {
			t.Errorf("DefaultScoringWeights().%s is zero — a dead scoring term", f.Name)
		}
	}
}

// constantsBeforeExtraction is DefaultScoringWeights as it stood at
// eace88a, hand-transcribed and never regenerated — the fixed point the
// extraction is measured against. It must not be replaced by a call to
// DefaultScoringWeights: once that function reads the config file, a test
// comparing it to the config proves only that the file equals itself.
func constantsBeforeExtraction() clank.ScoringWeights {
	return clank.ScoringWeights{
		Temporal:          1.0 / 3,
		Topological:       1.0 / 3,
		Historical:        1.0 / 3,
		FreshnessHalfLife: 30 * 24 * time.Hour,
		GroundingNone:     0.3,
		GroundingOne:      0.7,
		GroundingMany:     1.0,
		Causal:            0.5,
		// Promoted out of causal.go by this track; same values.
		TemporalHalfLife:   30 * time.Minute,
		HistoricalAloneCap: 0.5,
	}
}

func TestLoadWeights_TheShippedDefaultsEqualTheConstantsTheyReplaced(t *testing.T) {
	t.Parallel()
	// Extraction is a refactor, not a tuning change. Every value that moves out
	// of Go ships identical, so a red goldenpath after this track means the
	// move was wrong — never that the engine was retuned in passing.
	got, err := clank.LoadWeightsFile(filepath.Join("..", "..", "config", "clank", "weights.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(constantsBeforeExtraction(), got); diff != "" {
		t.Error("shipped weights config drifted from the constants it replaced (-want +got)\n", diff)
	}
}

func TestLoadWeights_RejectsAPartialFileRatherThanZeroingTheMissingTerms(t *testing.T) {
	t.Parallel()
	// A zero ScoringWeights does not degrade gracefully — wiring.go:91 already
	// says so. GroundingMany at 0 multiplies every candidate's confidence to
	// zero and the engine proposes nothing at all, silently. Config in this
	// tree fails at load, never at first use; weights get the same treatment
	// as an endpoint whose scheme cannot be mapped to a wire.
	partial := map[string]string{
		"one term omitted":  "temporal: 0.33\ntopological: 0.33\n",
		"every term absent": "\n",
	}
	for name, body := range partial {
		t.Run("LoadWeights rejects a file with "+name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "weights.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := clank.LoadWeightsFile(path)
			if !errors.Is(err, clank.ErrIncompleteWeights) {
				t.Errorf("want ErrIncompleteWeights, got %v — a missing term silently becomes zero, and a zero grounding tier suppresses every proposal the engine would have made", err)
			}
		})
	}
}

// TestLoadWeights_AcceptsATermExplicitlySetToZero is the case the partial-file
// test above must NOT cover: staging every field as a pointer exists so
// LoadWeightsFile can tell "omitted" from "present, and 0.0" apart. Causal: 0
// is a real operator choice (turn the causal bonus off); a loader that
// treated any zero as missing couldn't let them say it.
func TestLoadWeights_AcceptsATermExplicitlySetToZero(t *testing.T) {
	t.Parallel()
	want := zeroed(constantsBeforeExtraction(), "Causal")
	path := filepath.Join(t.TempDir(), "weights.yaml")
	if err := os.WriteFile(path, []byte(mustMarshal(t, want)), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := clank.LoadWeightsFile(path)
	if err != nil {
		t.Fatalf("LoadWeightsFile rejected an explicit zero term: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("explicit zero term did not round-trip (-want +got)\n", diff)
	}
}

// weightsFileFrom builds the plain map LoadWeightsFile's YAML shape expects,
// keyed the same as weightsFile's json tags — clank_test can't construct
// weightsFile directly, it's unexported, so this is the map that would
// marshal to the same document.
func weightsFileFrom(w clank.ScoringWeights) map[string]any {
	return map[string]any{
		"temporal":           w.Temporal,
		"topological":        w.Topological,
		"historical":         w.Historical,
		"freshnessHalfLife":  w.FreshnessHalfLife.String(),
		"groundingNone":      w.GroundingNone,
		"groundingOne":       w.GroundingOne,
		"groundingMany":      w.GroundingMany,
		"causal":             w.Causal,
		"temporalHalfLife":   w.TemporalHalfLife.String(),
		"historicalAloneCap": w.HistoricalAloneCap,
	}
}

func mustMarshal(t *testing.T, w clank.ScoringWeights) string {
	t.Helper()
	b, err := yaml.Marshal(weightsFileFrom(w))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// zeroed returns w with field set to its zero value — used to construct a
// weights file where a term is explicitly 0.0, the case LoadWeightsFile must
// accept (it's a value) as opposed to omit (which it must reject).
func zeroed(w clank.ScoringWeights, field string) clank.ScoringWeights {
	v := reflect.ValueOf(&w).Elem()
	v.FieldByName(field).SetZero()
	return w
}
