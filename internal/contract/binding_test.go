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
	cat := configtest.ShippedCatalog(t)

	var want []string
	for _, c := range cat.Contracts() {
		want = append(want, c.Name)
	}
	slices.Sort(want)

	bound, err := actuate.BoundRefs(cat, true)
	if err != nil {
		t.Fatalf("the shipped catalog holds an action with no executable mechanism: %v", err)
	}

	if diff := cmp.Diff(want, bound); diff != "" {
		t.Error("catalogued actions with no actuator binding — proposable and governable, but never executable (-want +got):", diff)
	}
}
