package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// activeSegmentName is the fixed name of a beat/subject's open segment —
// fixed so a restarted process can find (and recover) whatever a previous
// process left behind.
const activeSegmentName = "active.jsonl"

// Defaults for a WAL that doesn't set its own bounds.
const (
	defaultMaxBytes     = 64 * 1024 * 1024
	defaultMaxAge       = 10 * time.Minute
	defaultSyncInterval = 5 * time.Second
)

// ErrWALNotImplemented is returned by a WAL codepath that has no
// implementation yet.
var ErrWALNotImplemented = errors.New("wal: not implemented")

// activeFile is the WAL's seam onto its active segment's handle. *os.File
// satisfies it in production; tests fake it to observe Sync calls without a
// real disk fsync (AGENTS.md's Tools Seam: avoid a concrete *os.File).
type activeFile interface {
	io.Writer
	Sync() error
	Close() error
}

// WAL is a per-beat, per-subject append-only journal: every boundary object a
// beat publishes is appended as one JSON line to an active segment, which
// seals (fsync + rename, immutable) once it crosses a size or age bound. A
// crash can lose at most the unsynced tail of the active segment; it can
// never corrupt a sealed one.
type WAL struct {
	// Dir, Beat, and Subject together locate this WAL's segment directory
	// (Dir/Beat/Subject) — one WAL per beat per subject, so two beats or
	// two subjects never share a segment sequence.
	Dir, Beat, Subject string
	// MaxBytes seals the active segment once it reaches this size.
	// Defaults to defaultMaxBytes (64MiB) when <= 0.
	MaxBytes int64
	// MaxAge seals the active segment once it's been open this long, even
	// if MaxBytes hasn't been reached — bounds how much a slow-writing beat
	// can lose to an unsynced tail. Defaults to defaultMaxAge (10m) when
	// <= 0.
	MaxAge time.Duration
	// SyncInterval is the background fsync cadence for the active segment,
	// independent of Append. Defaults to defaultSyncInterval (5s) when
	// <= 0.
	SyncInterval time.Duration

	// openActive is the file-open seam; nil means "use the real filesystem."
	// Only a white-box (package publish) test can set it.
	openActive func(path string) (activeFile, error)

	mu         sync.Mutex
	active     activeFile
	size       int64
	openedAt   time.Time
	baseOffset int64

	startSyncLoop sync.Once
	stopSyncLoop  chan struct{}
	syncLoopDone  chan struct{}
}

// Append journals obj as one JSON line to the active segment, recovering an
// orphaned active segment from a previous process first if one exists.
func (w *WAL) Append(ctx context.Context, obj any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.ensureActive(); err != nil {
		return err
	}

	maxAge := w.MaxAge
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	if time.Since(w.openedAt) >= maxAge {
		if err := w.seal(); err != nil {
			return err
		}
	}

	line, err := encodeLine(obj)
	if err != nil {
		return err
	}
	n, err := w.active.Write(line)
	if err != nil {
		return fmt.Errorf("wal: write: %w", err)
	}
	w.size += int64(n)

	maxBytes := w.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	if w.size >= maxBytes {
		if err := w.seal(); err != nil {
			return err
		}
	}
	return nil
}

// Close stops the WAL's background sync timer, if one is running.
func (w *WAL) Close(_ context.Context) error {
	w.mu.Lock()
	stopCh := w.stopSyncLoop
	doneCh := w.syncLoopDone
	active := w.active
	w.active = nil
	w.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
		<-doneCh
	}

	if active == nil {
		return nil
	}
	if err := active.Sync(); err != nil {
		return fmt.Errorf("wal: close: sync: %w", err)
	}
	return active.Close()
}

// seal fsyncs and renames the active segment, making it immutable.
func (w *WAL) seal() error {
	if err := w.active.Sync(); err != nil {
		return fmt.Errorf("wal: seal: sync: %w", err)
	}
	if err := w.active.Close(); err != nil {
		return fmt.Errorf("wal: seal: close: %w", err)
	}

	dir := w.beatDir()
	activePath := filepath.Join(dir, activeSegmentName)
	sealedPath := filepath.Join(dir, segmentName(w.baseOffset, time.Now()))
	if err := os.Rename(activePath, sealedPath); err != nil {
		return fmt.Errorf("wal: seal: rename: %w", err)
	}

	w.baseOffset++
	w.active = nil
	return w.openFreshActive(activePath)
}

func (w *WAL) beatDir() string {
	return filepath.Join(w.Dir, w.Beat, w.Subject)
}

func (w *WAL) openFreshActive(path string) error {
	open := w.openActive
	if open == nil {
		open = defaultOpenActive
	}
	f, err := open(path)
	if err != nil {
		return fmt.Errorf("wal: open active segment: %w", err)
	}
	w.active = f
	w.size = 0
	w.openedAt = time.Now()
	return nil
}

func (w *WAL) recoverOrphan(dir, activePath string, base int64) error {
	raw, err := os.ReadFile(activePath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("wal: read orphaned active segment: %w", err)
	}
	complete := raw[:bytes.LastIndexByte(raw, '\n')+1]

	sealedPath := filepath.Join(dir, segmentName(base, time.Now()))
	f, err := os.OpenFile(sealedPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("wal: create revovered segment: %w", err)
	}
	if _, err := f.Write(complete); err != nil {
		_ = f.Close()
		return fmt.Errorf("wal: write recovered segment: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("wal: sync recovered segment: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("wal: close recovered segment: %w", err)
	}
	return os.Remove(activePath)
}

func (w *WAL) ensureActive() error {
	if w.active != nil {
		return nil
	}

	dir := w.beatDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("wal: mkdir %s: %w", dir, err)
	}

	base, err := nextBaseOffset(os.DirFS(dir))
	if err != nil {
		return fmt.Errorf("wal: next base offset: %w", err)
	}

	activePath := filepath.Join(dir, activeSegmentName)
	if _, err := os.Stat(activePath); err == nil {
		if err := w.recoverOrphan(dir, activePath, base); err != nil {
			return err
		}
		base++
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("wal: stat %s: %w", activePath, err)
	}
	w.baseOffset = base

	if err := w.openFreshActive(activePath); err != nil {
		return err
	}
	w.startSyncLoop.Do(func() {
		w.stopSyncLoop = make(chan struct{})
		w.syncLoopDone = make(chan struct{})
		go w.runSyncLoop()
	})
	return nil
}

func (w *WAL) runSyncLoop() {
	defer close(w.syncLoopDone)
	interval := w.SyncInterval
	if interval <= 0 {
		interval = defaultSyncInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			if w.active != nil {
				_ = w.active.Sync()
			}
			w.mu.Unlock()
		case <-w.stopSyncLoop:
			return
		}
	}
}

func defaultOpenActive(path string) (activeFile, error) {
	return os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
}

// segmentName is the immutable filename for a segment that started at
// baseOffset and was sealed at sealedAt.
func segmentName(baseOffset int64, sealedAt time.Time) string {
	return fmt.Sprintf("%020d-%d%s", baseOffset, sealedAt.UnixNano(), segmentExt)
}

// parseSegmentName parses a sealed segment's filename back into its
// baseOffset and sealedAt, reporting ok=false for anything that isn't one —
// including the active segment's own name.
func parseSegmentName(name string) (baseOffset int64, sealedAt time.Time, ok bool) {
	trimmed, found := strings.CutSuffix(name, segmentExt)
	if !found {
		return 0, time.Time{}, false
	}
	offsetPart, nanosPart, found := strings.Cut(trimmed, "-")
	if !found {
		return 0, time.Time{}, false
	}
	offset, err := strconv.ParseInt(offsetPart, 10, 64)
	if err != nil {
		return 0, time.Time{}, false
	}
	nanos, err := strconv.ParseInt(nanosPart, 10, 64)
	if err != nil {
		return 0, time.Time{}, false
	}
	return offset, time.Unix(0, nanos).UTC(), true
}

const segmentExt = ".jsonl"

// nextBaseOffset counts the sealed segments already present in fsys, so a
// (re)started WAL numbers its next segment after them.
func nextBaseOffset(fsys fs.FS) (int64, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return 0, fmt.Errorf("wal: next base offset: %w", err)
	}
	var count int64
	for _, entry := range entries {
		if _, _, ok := parseSegmentName(entry.Name()); ok {
			count++
		}
	}
	return count, nil
}

// encodeLine JSON-encodes obj as one newline-terminated WAL line.
func encodeLine(obj any) ([]byte, error) {
	line, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("wal: encode line: %w", err)
	}
	return append(line, '\n'), nil
}

// WALPublisher wraps another Publisher, journaling every object to the WAL
// before handing it to Next — the record must exist before the fact travels.
type WALPublisher[T any] struct {
	// WAL is journaled first — if Append fails, Next is never called, so
	// the WAL and the delivered stream never disagree about what was
	// journaled.
	WAL *WAL
	// Next is the Publisher the object reaches once the WAL append
	// succeeds — JetPublisher in production, a fake in tests.
	Next Publisher[T]
}

// Publish appends obj to WAL, then delegates to Next — in that order,
// so a crash between the two loses at most a delivery already durable on
// disk, never a delivery with no record of it at all.
func (p *WALPublisher[T]) Publish(ctx context.Context, subject string, obj T) error {
	if err := p.WAL.Append(ctx, obj); err != nil {
		return fmt.Errorf("wal publisher: append: %w", err)
	}
	if err := p.Next.Publish(ctx, subject, obj); err != nil {
		return fmt.Errorf("wal publisher: next: %w", err)
	}
	return nil
}
