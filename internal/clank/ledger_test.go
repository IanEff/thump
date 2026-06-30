package clank_test

import (
	"context"
	"testing"
	"time"

	"github.com/ianeff/clank/internal/clank"
)

func TestProposalLog_OpenRespectsTheDedupeWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := clank.NewMemProposalLog()
	at := time.Now()
	if err := log.Record(ctx, clank.ProposalSet{SignalRef: "fp-1", Status: &clank.ProposalStatus{Phase: "proposed"}}); err != nil {
		t.Fatal(err)
	}

	in, err := log.Open(ctx, "fp-1", at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 {
		t.Errorf("recorded in-window should be open: want 1, got %d", len(in))
	}
	out, err := log.Open(ctx, "fp-1", at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("older than `since` should not be open: want 0, got %d", len(out))
	}
}

func TestProposalLog_OpenIgnoresClosedSets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := clank.NewMemProposalLog()
	if err := log.Record(ctx, clank.ProposalSet{
		SignalRef: "fp-1",
		Status:    &clank.ProposalStatus{Phase: "closed"},
	}); err != nil {
		t.Fatal(err)
	}
	open, err := log.Open(ctx, "fp-1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("a closed set must not suppress a new one: want 0, got %d", len(open))
	}
}
