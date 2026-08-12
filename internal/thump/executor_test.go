package thump_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/thump"
)

func TestDryRun_StampsAnHonestOutcome(t *testing.T) {
	t.Parallel()

	got := thump.DryRun{}.Execute(context.Background(), goldenOrder(), frozenNow())

	if err := got.Auditable(); err != nil {
		t.Fatal("every executor result must be auditable:", err)
	}
	if diff := cmp.Diff(goldenOutcome(), got); diff != "" {
		t.Error("dry-run outcome drifted from the golden fixture (-want +got)", diff)
	}
}
