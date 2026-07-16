package contract_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/contract"
)

func TestDefault_EveryActuatableActionHasABlastTier(t *testing.T) {
	t.Parallel()
	var missing []string
	for _, ref := range actuate.BoundRefs() {
		c, ok := contract.Default().ByName(ref)
		if !ok {
			t.Fatalf("actuate.BoundRefs names %q, but contract.Default() has no such contract", ref)
		}
		if c.BlastTier == "" {
			missing = append(missing, ref)
		}
	}
	if diff := cmp.Diff([]string(nil), missing); diff != "" {
		t.Error("actuatable actions with no authored blastTier (-want +got):", diff)
	}
}
