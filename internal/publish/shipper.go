package publish

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// SegmentSink is where a sealed WAL segment goes once WAL.Ship uploads it —
// an object-store bucket in production (S3SegmentSink), a fake in tests.
// Ship depends on this interface, never a concrete client.
type SegmentSink interface {
	Put(ctx context.Context, key string, r io.Reader) error
}

// SealedSegments lists this WAL's sealed segment files, oldest first — never
// the active segment, which is still being appended to. The zero-padded
// baseOffset prefix in each filename (segmentName) makes lexical order the
// same as seal order, so a plain sort.Strings is enough.
func (w *WAL) SealedSegments() ([]string, error) {
	dir := w.beatDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		// ensureActive only MkdirAll's this dir on the WAL's first Append —
		// a WAL that's never been written to has no directory yet, which is
		// "zero sealed segments," not a failure. RunShipper polls on a fixed
		// interval from process start, before that first write may happen.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wal: sealed segments: %w", err)
	}
	var out []string
	for _, e := range entries {
		if _, _, ok := parseSegmentName(e.Name()); ok {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Ship uploads every currently sealed segment to sink — keyed
// <Beat>/<Subject>/<segment file name>, the layout segmentDir's tests
// already mirror — and removes each one locally once its upload succeeds.
// This is the Mimir pattern: the WAL is durability for what hasn't shipped
// yet, not a permanent archive. A segment that fails to upload is left in
// place for the next Ship call to retry; a segment already shipped is
// already gone from disk, so a repeat Ship call is a silent no-op — the
// same "redelivery is boring" property the broker side leans on.
func (w *WAL) Ship(ctx context.Context, sink SegmentSink) error {
	segments, err := w.SealedSegments()
	if err != nil {
		return err
	}
	for _, path := range segments {
		if err := w.shipOne(ctx, sink, path); err != nil {
			return err
		}
	}
	return nil
}

func (w *WAL) shipOne(ctx context.Context, sink SegmentSink, path string) error {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return fmt.Errorf("wal: ship: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	key := w.Beat + "/" + w.Subject + "/" + filepath.Base(path)
	if err := sink.Put(ctx, key, f); err != nil {
		return fmt.Errorf("wal: ship: put %s: %w", key, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("wal: ship: remove %s: %w", path, err)
	}
	return nil
}
