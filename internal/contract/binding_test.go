package contract_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/configtest"
)

func TestShippedCatalog_EveryCatalogedActionIsActuatorBound(t *testing.T) {
	t.Parallel()

	// configtest.ShippedCatalog defaults to thump-test alone — looping every
	// profile here is what stops a profile-only catalog regression (a dev-only
	// action authored with no actuator binding) from shipping green.
	profiles := []string{"thump-test", "dev"}

	for _, profile := range profiles {
		t.Run("profile "+profile+" catalogs every action against an actuator", func(t *testing.T) {
			t.Parallel()
			cat := configtest.CatalogForProfile(t, profile)

			var want []string
			for _, c := range cat.Contracts() {
				want = append(want, c.Name)
			}
			slices.Sort(want)

			bound, err := actuate.BoundRefs(cat)
			if err != nil {
				t.Fatalf("the %s catalog holds an action with no executable mechanism: %v", profile, err)
			}

			if diff := cmp.Diff(want, bound); diff != "" {
				t.Error("catalogued actions with no actuator binding — proposable and governable, but never executable (-want +got):", diff)
			}
		})
	}
}
