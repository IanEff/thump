package clank_test

import (
	"context"
	"testing"

	"github.com/ianeff/clank/internal/clank"
)

func TestStore_PendingReturnsACheckpointedTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := clank.NewMemStore()
	want := clank.Turn{RunID: "r1", Step: 0, Msgs: []clank.Message{{Role: "user", Content: "hi"}}}
	if err := store.Checkpoint(ctx, want); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RunID != "r1" {
		t.Errorf("a checkpointed turn should come back as pending: want [r1], got %+v", pending)
	}
}

func TestStore_FinishRemovesARunFromPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := clank.NewMemStore()
	if err := store.Checkpoint(ctx, clank.Turn{RunID: "r1", Step: 0}); err != nil {
		t.Fatal(err)
	}

	if err := store.Finish(ctx, "r1", nil); err != nil {
		t.Fatal(err)
	}

	pending, err := store.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("a finished run should not be pending: want 0, got %d", len(pending))
	}
}
