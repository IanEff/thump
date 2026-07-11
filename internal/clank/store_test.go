package clank_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/s3test"
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

func TestDirStore_CheckpointedTurnsRoundTripInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := clank.NewDirStore(dir)

	turn0 := clank.Turn{RunID: "r1", Step: 0, Msgs: []clank.Message{{Role: "user", Content: "investigate"}}}
	turn1 := clank.Turn{RunID: "r1", Step: 1, Msgs: []clank.Message{{Role: "assistant", Content: "proposing"}}}

	if err := store.Checkpoint(ctx, turn0); err != nil {
		t.Fatal(err)
	}
	if err := store.Checkpoint(ctx, turn1); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, filepath.Join(dir, "r1.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("want 2 checkpointed lines, got %d: %q", len(lines), lines)
	}

	var got0, got1 clank.Turn
	if err := json.Unmarshal([]byte(lines[0]), &got0); err != nil {
		t.Fatalf("decode line 0: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &got1); err != nil {
		t.Fatalf("decode line 1: %v", err)
	}

	if diff := cmp.Diff(turn0, got0); diff != "" {
		t.Errorf("step 0 didn't round-trip (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(turn1, got1); diff != "" {
		t.Errorf("step 1 didn't round-trip (-want +got):\n%s", diff)
	}
}

func TestDirStore_FinishAppendsACleanTerminalMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := clank.NewDirStore(dir)

	if err := store.Checkpoint(ctx, clank.Turn{RunID: "r1", Step: 0}); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, "r1", nil); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, filepath.Join(dir, "r1.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("want checkpoint + terminal marker, got %d lines: %q", len(lines), lines)
	}

	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("decode terminal marker: %v", err)
	}
	if finished, _ := last["finished"].(bool); !finished {
		t.Errorf("want a finished:true terminal marker, got %v", last)
	}
	if _, hasErr := last["error"]; hasErr {
		t.Errorf("a clean finish shouldn't carry an error field, got %v", last)
	}
}

func TestDirStore_FinishRecordsARunError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := clank.NewDirStore(dir)

	if err := store.Finish(ctx, "r2", errors.New("model timed out")); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, filepath.Join(dir, "r2.jsonl"))
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("decode terminal marker: %v", err)
	}
	if got := last["error"]; got != "model timed out" {
		t.Errorf("want error %q recorded, got %v", "model timed out", got)
	}
}

func TestS3Store_CheckpointThenPersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, bucket := s3test.New(t)
	store := clank.NewS3Store(client, bucket)

	want := clank.Turn{RunID: "r1", Step: 0, Msgs: []clank.Message{{Role: "user", Content: "investigate"}}}
	if err := store.Checkpoint(ctx, want); err != nil {
		t.Fatal(err)
	}

	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("transcripts/r1/0.json"),
	})
	if err != nil {
		t.Fatalf("get persisted turn: %v", err)
	}
	defer func() { _ = out.Body.Close() }()

	raw, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read persisted turn: %v", err)
	}
	var got clank.Turn
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode persisted turn: %v", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("checkpointed turn didn't round-trip (-want +got):\n%s", diff)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("read transcript %s: %v", path, err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}
