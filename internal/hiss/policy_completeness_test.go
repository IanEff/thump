package hiss_test

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/configtest"
	"github.com/ianeff/thump/internal/hiss"
)

// TestPolicy_FloorsCoverEveryActuatableClass is the confidence-floor
// completeness guard: a (tier, class) pair the actuator can really fire
// with no Policy.Floors entry clears hiss's confidence-floor veto on any
// nonzero Confidence at all, leaning on the reasoner's judgment instead of
// a real minimum (I-6). Keyed off actuate.BoundRefs, not the full catalog —
// a catalogued-but-unbound ref can't hurt anyone yet (that gap is G4a's).
func TestPolicy_FloorsCoverEveryActuatableClass(t *testing.T) {
	t.Parallel()
	pol, err := hiss.LoadPolicy(filepath.Join("..", "..", "config", "hiss", "policy.yaml"))
	if err != nil {
		t.Fatalf("load shipped policy: %v", err)
	}
	cat := configtest.ShippedCatalog(t)

	bound, err := actuate.BoundRefs(cat, true)
	if err != nil {
		t.Fatalf("the shipped catalog holds an action with no executable mechanism: %v", err)
	}

	var missing []string
	for _, ref := range bound {
		c, ok := cat.ByName(ref)
		if !ok {
			t.Fatalf("actuate.BoundRefs names %q, but the shipped catalog has no such contract", ref)
		}
		for _, tier := range c.ApplicableTiers {
			for _, class := range c.ApplicableFailureClasses {
				if pol.Floors[tier][class] <= 0 {
					missing = append(missing, tier+"/"+string(class)+" (via "+ref+")")
				}
			}
		}
	}

	if diff := cmp.Diff([]string(nil), missing); diff != "" {
		t.Error("actuatable classes with no confidence floor (-want +got):\n", diff)
	}
}
