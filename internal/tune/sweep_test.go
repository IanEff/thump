package tune_test

import (
	"context"
	"testing"

	"github.com/ianeff/thump/internal/tune"
)

// TestRun_DropsATranscriptThatFailsToReplayAndStillReturnsGridPoints pins the
// fix for truncated.jsonl aborting the whole sweep: replay's own truncation
// fixture is deliberately unplayable, and one bad transcript must not stop
// every other transcript in the corpus from being swept.
func TestRun_DropsATranscriptThatFailsToReplayAndStillReturnsGridPoints(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{Transcripts: []tune.TranscriptPaths{
		{JSONL: "../replay/testdata/slo_burn-cart.jsonl", Set: "../replay/testdata/slo_burn-cart.set.json"},
		{JSONL: "../replay/testdata/truncated.jsonl", Set: "../replay/testdata/truncated.set.json"},
	}}

	points, err := tune.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) == 0 {
		t.Error("want grid points from the surviving transcript, got none")
	}
}

// TestRun_ErrorsWhenEveryTranscriptFailsToReplay pins the other side: a
// sweep whose entire corpus is unplayable must fail loudly rather than
// silently reporting an empty, meaningless grid.
func TestRun_ErrorsWhenEveryTranscriptFailsToReplay(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{Transcripts: []tune.TranscriptPaths{
		{JSONL: "../replay/testdata/truncated.jsonl", Set: "../replay/testdata/truncated.set.json"},
	}}

	if _, err := tune.Run(context.Background(), cfg); err == nil {
		t.Error("want an error when no transcript in the corpus can replay, got nil")
	}
}
