package contract_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/configtest"
)

func TestShippedCatalog_EveryActuatableActionHasABlastTier(t *testing.T) {
	t.Parallel()
	cat := configtest.ShippedCatalog(t)
	bound, err := actuate.BoundRefs(cat, true)
	if err != nil {
		t.Fatalf("the shipped catalog holds an action with no executable mechanism: %v", err)
	}

	var missing []string
	for _, ref := range bound {
		c, ok := cat.ByName(ref)
		if !ok {
			t.Fatalf("actuate.BoundRefs names %q, but config/actions/catalog.yaml has no such contract", ref)
		}
		if c.BlastTier == "" {
			missing = append(missing, ref)
		}
	}
	if diff := cmp.Diff([]string(nil), missing); diff != "" {
		t.Error("actuatable actions with no authored blastTier (-want +got):", diff)
	}
}
