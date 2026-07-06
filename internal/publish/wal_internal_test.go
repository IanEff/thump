package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// dummyRecord stands in for a real boundary object wherever the object's
// shape doesn't matter to the behavior under test.
type dummyRecord struct{ ID string }

func TestSegmentName_RoundTripsThroughParseSegmentName(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		baseOffset int64
		sealedAt   time.Time
	}{
		"round-trips a zero base offset":         {0, time.Unix(0, 0).UTC()},
		"round-trips a large base offset":        {123456789, time.Unix(0, 0).UTC()},
		"round-trips a real sealed-at timestamp": {42, time.Date(2026, 7, 6, 9, 6, 12, 0, time.UTC)},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := segmentName(tc.baseOffset, tc.sealedAt)

			gotOffset, gotSealedAt, ok := parseSegmentName(got)
			if !ok {
				t.Fatalf("parseSegmentName(%q) ok = false, want true", got)
			}
			if diff := cmp.Diff(tc.baseOffset, gotOffset); diff != "" {
				t.Error("wrong base offset round-tripped", diff)
			}
			if !gotSealedAt.Equal(tc.sealedAt) {
				t.Errorf("wrong sealedAt round-tripped: want %v, got %v", tc.sealedAt, gotSealedAt)
			}
		})
	}
}

func TestSegmentName_SortsLexicographicallyByBaseOffset(t *testing.T) {
	t.Parallel()
	sealedAt := time.Date(2026, 7, 6, 9, 6, 12, 0, time.UTC)

	earlier := segmentName(1, sealedAt)
	later := segmentName(2, sealedAt)

	if earlier >= later {
		t.Errorf("segmentName(1, ...) = %q, segmentName(2, ...) = %q; want the lower offset to sort first", earlier, later)
	}
}

func TestParseSegmentName_RejectsNamesThatArentSealedSegments(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"rejects the active segment's own filename":        activeSegmentName,
		"rejects a name with no offset-sealedAt separator": "garbage.jsonl",
		"rejects a non-numeric base offset":                "not-a-number-123.jsonl",
		"rejects a name missing the .jsonl extension":      "00000000000000000001-123",
		"rejects the empty string":                         "",
	}
	for name, filename := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, ok := parseSegmentName(filename); ok {
				t.Errorf("parseSegmentName(%q) ok = true, want false", filename)
			}
		})
	}
}

func TestNextBaseOffset_CountsSealedSegmentsOnly(t *testing.T) {
	t.Parallel()
	sealedAt := time.Unix(1751792772, 0).UTC()
	tests := map[string]struct {
		fsys fstest.MapFS
		want int64
	}{
		"an empty directory starts at offset zero": {
			fsys: fstest.MapFS{},
			want: 0,
		},
		"one sealed segment yields offset one": {
			fsys: fstest.MapFS{
				segmentName(0, sealedAt): &fstest.MapFile{},
			},
			want: 1,
		},
		"the active segment does not count as sealed": {
			fsys: fstest.MapFS{
				segmentName(0, sealedAt): &fstest.MapFile{},
				activeSegmentName:        &fstest.MapFile{},
			},
			want: 1,
		},
		"three sealed segments yield offset three": {
			fsys: fstest.MapFS{
				segmentName(0, sealedAt):                    &fstest.MapFile{},
				segmentName(1, sealedAt.Add(time.Second)):   &fstest.MapFile{},
				segmentName(2, sealedAt.Add(2*time.Second)): &fstest.MapFile{},
			},
			want: 3,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := nextBaseOffset(tc.fsys)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong next base offset", diff)
			}
		})
	}
}

func TestEncodeLine_JSONEncodesWithATrailingNewline(t *testing.T) {
	t.Parallel()
	want := dummyRecord{ID: "r1"}

	got, err := encodeLine(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1] != '\n' {
		t.Fatalf("encodeLine() = %q, want a trailing newline", got)
	}

	var roundTripped dummyRecord
	if err := json.Unmarshal(got[:len(got)-1], &roundTripped); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, roundTripped); diff != "" {
		t.Error("wrong object round-tripped", diff)
	}
}

func TestEncodeLine_ErrorsOnAValueJSONCannotMarshal(t *testing.T) {
	t.Parallel()
	_, err := encodeLine(make(chan int))
	if err == nil {
		t.Fatal("encodeLine(chan int) error = nil, want non-nil")
	}
}

// fakeActiveFile is the activeFile double: it counts Sync calls so a test can
// prove the periodic timer fired without touching a real disk fsync.
type fakeActiveFile struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	syncs  int
	closed bool
}

func (f *fakeActiveFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Write(p)
}

func (f *fakeActiveFile) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	return nil
}

func (f *fakeActiveFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeActiveFile) syncCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.syncs
}

// TestWAL_SyncsTheActiveSegmentOnA5SecondTimerEvenWithoutFurtherAppends is the
// one behavior that can't be proven by reading the file back (a plain
// os.File.Write already lands in the OS page cache — fsync only changes the
// durability guarantee, not what ReadFile sees). It needs the activeFile seam
// and a white-box test to inject it.
func TestWAL_SyncsTheActiveSegmentOnA5SecondTimerEvenWithoutFurtherAppends(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeActiveFile{}
		w := &WAL{
			Dir: t.TempDir(), Beat: "rattle", Subject: "thump.detections",
			SyncInterval: 5 * time.Second,
			openActive:   func(string) (activeFile, error) { return fake, nil },
		}
		defer func() { _ = w.Close(context.Background()) }()

		if err := w.Append(context.Background(), dummyRecord{ID: "r1"}); err != nil {
			t.Fatal(err)
		}
		if got := fake.syncCount(); got != 0 {
			t.Fatalf("Sync called %d times immediately after Append, want 0 (not per-append)", got)
		}

		time.Sleep(6 * time.Second)
		synctest.Wait()

		if got := fake.syncCount(); got != 1 {
			t.Errorf("Sync called %d times after SyncInterval elapsed with no further appends, want 1", got)
		}
	})
}

func TestWAL_ClosePreventsFurtherBackgroundSyncs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeActiveFile{}
		w := &WAL{
			Dir: t.TempDir(), Beat: "rattle", Subject: "thump.detections",
			SyncInterval: 5 * time.Second,
			openActive:   func(string) (activeFile, error) { return fake, nil },
		}

		if err := w.Append(context.Background(), dummyRecord{ID: "r1"}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !fake.closed {
			t.Error("Close() must close the active segment's file handle")
		}

		syncsAtClose := fake.syncCount()
		time.Sleep(30 * time.Second)
		synctest.Wait()

		if got := fake.syncCount(); got != syncsAtClose {
			t.Errorf("Sync called %d more times after Close, want 0 more", got-syncsAtClose)
		}
	})
}
