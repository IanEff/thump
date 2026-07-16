package hiss_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/contract"
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
	pol := loadShippedPolicy(t)
	cat := loadShippedCatalog(t)

	var missing []string
	for _, ref := range actuate.BoundRefs() {
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

func loadShippedPolicy(t *testing.T) hiss.Policy {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "hiss", "policy.yaml")) //nolint:gosec
	if err != nil {
		t.Fatalf("read shipped policy: %v", err)
	}
	var pol hiss.Policy
	if err := yaml.Unmarshal(raw, &pol); err != nil {
		t.Fatalf("unmarshal shipped policy: %v", err)
	}
	return pol
}

func loadShippedCatalog(t *testing.T) *contract.StaticCatalog {
	t.Helper()
	cat, err := contract.LoadCatalogFile(filepath.Join("..", "..", "config", "actions", "catalog.yaml"), contract.Preconditions)
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	return cat
}
