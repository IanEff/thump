package clank_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/clank"
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
