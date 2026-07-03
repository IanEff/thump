package clank_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ianeff/clank/internal/clank"
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
