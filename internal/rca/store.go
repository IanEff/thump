package rca

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/ianeff/thump/internal/clank"
)

// caseStore writes every Turn of a graded run to <dir>/<stem>.jsonl, one file
// per Case, truncated at the run's first checkpoint. clank.DirStore keys its
// filename off the engine's runID — a fingerprint plus a nanosecond stamp, so
// two runs of one signal never collide — but the graded corpus wants the
// opposite: one checked-in transcript per Case, sharing its stem with the
// .set.json beside it, because that shared stem is the only pairing a sweep
// can resolve.
type caseStore struct {
	mu      sync.Mutex
	path    string
	started bool
}

func newCaseStore(dir, fixture string) *caseStore {
	return &caseStore{path: transcriptPath(dir, fixture)}
}

// Checkpoint appends t as one JSON line to the case's transcript.
func (s *caseStore) Checkpoint(ctx context.Context, t clank.Turn) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.appendLine(t)
}

// Pending always returns nil — the graded suite re-runs a case from scratch
// rather than resuming one, same as clank.DirStore.
func (s *caseStore) Pending(ctx context.Context) ([]clank.Turn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, nil
}

// Finish appends a terminal record carrying no RunID — replay skips it when
// recovering the conversation, so it marks the run without joining it.
func (s *caseStore) Finish(ctx context.Context, _ string, runErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	rec := struct {
		Finished bool   `json:"finished"`
		Error    string `json:"error,omitempty"`
	}{Finished: true}
	if runErr != nil {
		rec.Error = runErr.Error()
	}
	return s.appendLine(rec)
}

func (s *caseStore) appendLine(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("case store: marshal: %w", err)
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if !s.started {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(s.path, flags, 0o600) //nolint:gosec // G304: transcripts comes from a CLI flag, env var, or os.TempDir — operator-supplied, not user input
	if err != nil {
		return fmt.Errorf("case store: open %s: %w", s.path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("case store: write %s: %w", s.path, err)
	}
	s.started = true

	return nil
}
