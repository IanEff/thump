package publish_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/publish"
)

// segmentDir is where a WAL{Dir, Beat, Subject} actually writes — mirrors the
// MinIO key shape (<beat>/<subject>/...) the guide fixes for Stage 5, so the
// on-disk layout doesn't have to change again when uploads land.
func segmentDir(root, beat, subject string) string {
	return filepath.Join(root, beat, subject)
}

func TestWAL_AppendWritesAJSONLineThatRoundTrips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections"}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	want := signal.Detection{Fingerprint: "fp-1"}

	if err := w.Append(context.Background(), want); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(segmentDir(dir, "rattle", "thump.detections"), "active.jsonl")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var got signal.Detection
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("active.jsonl did not round-trip: %v\ncontent: %s", err, raw)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong object round-tripped from the active segment", diff)
	}
}

func TestWAL_SealsTheActiveSegmentOnceItCrossesMaxBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections", MaxBytes: 1}
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	beatDir := segmentDir(dir, "rattle", "thump.detections")
	want := signal.Detection{Fingerprint: "fp-1"}

	if err := w.Append(context.Background(), want); err != nil {
		t.Fatal(err)
	}

	sealed, err := filepath.Glob(filepath.Join(beatDir, "*-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 1 {
		t.Fatalf("got %d sealed segments after crossing MaxBytes, want 1", len(sealed))
	}

	rawSealed, err := os.ReadFile(sealed[0]) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var gotSealed signal.Detection
	if err := json.Unmarshal(rawSealed, &gotSealed); err != nil {
		t.Fatalf("sealed segment did not round-trip: %v\ncontent: %s", err, rawSealed)
	}
	if diff := cmp.Diff(want, gotSealed); diff != "" {
		t.Error("wrong object in the sealed segment", diff)
	}

	if _, err := os.Stat(filepath.Join(beatDir, "active.jsonl")); err != nil {
		t.Errorf("a fresh active segment must exist after a seal: %v", err)
	}
}

func TestWAL_SealsTheActiveSegmentAfterMaxAgeElapses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections", MaxAge: time.Minute}
		defer func() { _ = w.Close(context.Background()) }()
		beatDir := segmentDir(dir, "rattle", "thump.detections")

		first := signal.Detection{Fingerprint: "fp-1"}
		if err := w.Append(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		if sealed, _ := filepath.Glob(filepath.Join(beatDir, "*-*.jsonl")); len(sealed) != 0 {
			t.Fatalf("segment sealed before MaxAge elapsed: %v", sealed)
		}

		time.Sleep(2 * time.Minute)
		synctest.Wait()

		second := signal.Detection{Fingerprint: "fp-2"}
		if err := w.Append(context.Background(), second); err != nil {
			t.Fatal(err)
		}

		sealed, err := filepath.Glob(filepath.Join(beatDir, "*-*.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if len(sealed) != 1 {
			t.Fatalf("got %d sealed segments after MaxAge elapsed, want 1", len(sealed))
		}

		rawSealed, err := os.ReadFile(sealed[0]) //nolint:gosec
		if err != nil {
			t.Fatal(err)
		}
		var gotSealed signal.Detection
		if err := json.Unmarshal(rawSealed, &gotSealed); err != nil {
			t.Fatalf("sealed segment did not round-trip: %v\ncontent: %s", err, rawSealed)
		}
		if diff := cmp.Diff(first, gotSealed); diff != "" {
			t.Error("the stale segment (holding the first line) is what should have sealed", diff)
		}

		rawActive, err := os.ReadFile(filepath.Join(beatDir, "active.jsonl")) //nolint:gosec
		if err != nil {
			t.Fatal(err)
		}
		var gotActive signal.Detection
		if err := json.Unmarshal(rawActive, &gotActive); err != nil {
			t.Fatalf("fresh active segment did not round-trip: %v\ncontent: %s", err, rawActive)
		}
		if diff := cmp.Diff(second, gotActive); diff != "" {
			t.Error("the fresh active segment should hold only the second line", diff)
		}
	})
}

// TestWAL_RecoversAnOrphanedActiveSegmentWithoutLosingCompleteLines is the
// crash test: it simulates a previous process dying mid-write (a torn
// trailing line, no closing newline) and asserts the durability asymmetry —
// every complete line survives, sealed and parseable; only the torn tail is
// lost.
func TestWAL_RecoversAnOrphanedActiveSegmentWithoutLosingCompleteLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	beatDir := segmentDir(dir, "rattle", "thump.detections")
	if err := os.MkdirAll(beatDir, 0o750); err != nil {
		t.Fatal(err)
	}

	complete1, err := json.Marshal(signal.Detection{Fingerprint: "fp-1"})
	if err != nil {
		t.Fatal(err)
	}
	complete2, err := json.Marshal(signal.Detection{Fingerprint: "fp-2"})
	if err != nil {
		t.Fatal(err)
	}
	var orphan bytes.Buffer
	orphan.Write(complete1)
	orphan.WriteByte('\n')
	orphan.Write(complete2)
	orphan.WriteByte('\n')
	orphan.WriteString(`{"Fingerprint": "fp-3-torn`) // torn: no closing brace, no newline
	if err := os.WriteFile(filepath.Join(beatDir, "active.jsonl"), orphan.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	// "Restart": a fresh WAL value pointed at the same directory — nothing
	// carries over in memory, only what's on disk.
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections"}
	t.Cleanup(func() { _ = w.Close(context.Background()) })

	fourth := signal.Detection{Fingerprint: "fp-4"}
	if err := w.Append(context.Background(), fourth); err != nil {
		t.Fatal(err)
	}

	sealed, err := filepath.Glob(filepath.Join(beatDir, "*-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 1 {
		t.Fatalf("got %d sealed segments after recovery, want 1 (the recovered orphan)", len(sealed))
	}

	recovered, err := os.ReadFile(sealed[0]) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	wantRecovered := string(complete1) + "\n" + string(complete2) + "\n"
	if diff := cmp.Diff(wantRecovered, string(recovered)); diff != "" {
		t.Error("recovered segment must hold exactly the complete lines, torn tail dropped", diff)
	}

	rawActive, err := os.ReadFile(filepath.Join(beatDir, "active.jsonl")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var gotActive signal.Detection
	if err := json.Unmarshal(rawActive, &gotActive); err != nil {
		t.Fatalf("new active segment did not round-trip: %v\ncontent: %s", err, rawActive)
	}
	if diff := cmp.Diff(fourth, gotActive); diff != "" {
		t.Error("wrong object in the fresh active segment", diff)
	}
}

// FakePublisher (defined in publish_test.go) is reused here as WALPublisher's
// Next — this test cares about ordering, not FakePublisher's own contract.
type orderCheckingPublisher struct {
	beatDir       string
	called        bool
	sawWALContent bool
}

func (o *orderCheckingPublisher) Publish(_ context.Context, _ string, _ signal.Detection) error {
	o.called = true
	raw, err := os.ReadFile(filepath.Join(o.beatDir, "active.jsonl")) //nolint:gosec
	o.sawWALContent = err == nil && len(raw) > 0
	return nil
}

func TestWALPublisher_AppendsToTheWALBeforeDelegatingToNext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	beatDir := segmentDir(dir, "rattle", "thump.detections")
	w := &publish.WAL{Dir: dir, Beat: "rattle", Subject: "thump.detections"}
	t.Cleanup(func() { _ = w.Close(context.Background()) })

	next := &orderCheckingPublisher{beatDir: beatDir}
	pub := &publish.WALPublisher[signal.Detection]{WAL: w, Next: next}

	want := signal.Detection{Fingerprint: "fp-1"}
	if err := pub.Publish(context.Background(), "thump.detections", want); err != nil {
		t.Fatal(err)
	}

	if !next.called {
		t.Fatal("Next.Publish was never called")
	}
	if !next.sawWALContent {
		t.Error("Next.Publish ran before the WAL had the record on disk — append-then-publish order violated")
	}
}

func TestWALPublisher_PropagatesTheWALsAppendError(t *testing.T) {
	t.Parallel()
	// A WAL pointed at a file (not a directory) as its Dir can never create
	// the beat/subject directory beneath it — Append must fail, and
	// WALPublisher must surface that instead of calling Next anyway.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &publish.WAL{Dir: blocker, Beat: "rattle", Subject: "thump.detections"}
	t.Cleanup(func() { _ = w.Close(context.Background()) })

	next := &orderCheckingPublisher{beatDir: blocker}
	pub := &publish.WALPublisher[signal.Detection]{WAL: w, Next: next}

	err := pub.Publish(context.Background(), "thump.detections", signal.Detection{Fingerprint: "fp-1"})
	if err == nil {
		t.Fatal("Publish() error = nil, want non-nil when the WAL can't append")
	}
	if next.called {
		t.Error("Next.Publish must not run when the WAL append failed")
	}
}
