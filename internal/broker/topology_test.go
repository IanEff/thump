package broker_test

import (
	"context"
	"testing"

	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
)

func TestEnsureTopology_IsIdempotent(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx := context.Background()

	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal("first ensure:", err)
	}
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal("second ensure must be a no-op, not an error:", err)
	}

	// the stream exists and carries all five subjects
	s, err := js.Stream(ctx, "THUMP")
	if err != nil {
		t.Fatal("stream missing after ensure:", err)
	}
	for _, subj := range []string{"thump.detections", "thump.proposals", "thump.decisions", "thump.outcomes", "thump.declines"} {
		if _, err := s.Consumer(ctx, broker.DurableFor(subj)); err != nil {
			t.Errorf("durable consumer for %s missing: %v", subj, err)
		}
	}
}
