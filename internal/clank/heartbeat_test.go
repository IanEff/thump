package clank_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ianeff/thump/internal/clank"
)

// spyStore counts Checkpoint calls and can be told to fail the next one —
// just enough surface to pin HeartbeatingStore's delegation without a real
// backend.
type spyStore struct {
	clank.Store
	checkpoints int
	failNext    bool
}

func (s *spyStore) Checkpoint(_ context.Context, _ clank.Turn) error {
	s.checkpoints++
	if s.failNext {
		return errors.New("checkpoint failed")
	}
	return nil
}

func TestHeartbeatingStore_PingsAfterASuccessfulCheckpoint(t *testing.T) {
	t.Parallel()
	pings := 0
	ctx := clank.WithHeartbeat(context.Background(), func() { pings++ })
	store := clank.HeartbeatingStore{Store: &spyStore{}}

	if err := store.Checkpoint(ctx, clank.Turn{RunID: "r1", Step: 0}); err != nil {
		t.Fatal(err)
	}
	if pings != 1 {
		t.Errorf("want 1 heartbeat ping after a successful checkpoint, got %d", pings)
	}
}

func TestHeartbeatingStore_SkipsThePingWhenCheckpointFails(t *testing.T) {
	t.Parallel()
	pings := 0
	ctx := clank.WithHeartbeat(context.Background(), func() { pings++ })
	store := clank.HeartbeatingStore{Store: &spyStore{failNext: true}}

	if err := store.Checkpoint(ctx, clank.Turn{RunID: "r1", Step: 0}); err == nil {
		t.Fatal("want the underlying store's error, got nil")
	}
	if pings != 0 {
		t.Errorf("want no heartbeat ping on a failed checkpoint — a failed write is not progress, got %d pings", pings)
	}
}

func TestHeartbeatingStore_IsANoOpWithoutAHeartbeatOnTheContext(t *testing.T) {
	t.Parallel()
	store := clank.HeartbeatingStore{Store: &spyStore{}}

	// No clank.WithHeartbeat anywhere on this ctx — e.g. the offline dir-poll
	// transport, or any test calling Propose directly. Checkpoint must not
	// panic reaching for a heartbeat that was never set.
	if err := store.Checkpoint(context.Background(), clank.Turn{RunID: "r1", Step: 0}); err != nil {
		t.Fatal(err)
	}
}
