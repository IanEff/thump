package contract_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
)

func TestShippedCatalog_EveryCatalogedActionIsActuatorBound(t *testing.T) {
	t.Parallel()
	bound := map[string]bool{}
	for _, r := range actuate.BoundRefs() {
		bound[r] = true
	}

	var unbound []string
	for _, c := range loadShippedCatalog(t).Contracts() {
		if !bound[c.Name] {
			unbound = append(unbound, c.Name)
		}
	}

	if diff := cmp.Diff([]string(nil), unbound); diff != "" {
		t.Error("catalogued actions with no actuator binding — proposable and governable, but never executable (-want +got):", diff)
	}
}
