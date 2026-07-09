package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store is the reason loop's checkpoint memory — one Turn per model
// completion, not the proposal ledger (MemProposalLog): a different lifetime
// and a different granularity. Checkpoint must succeed before the loop takes
// its next turn; a checkpoint error halts the run rather than risk a turn
// nothing remembers. Because Propose never mutates infrastructure,
// re-running a halted signal from scratch is always safe.
type Store interface {
	Checkpoint(context.Context, Turn) error
	Pending(context.Context) ([]Turn, error)
	Finish(context.Context, string, error) error
}

// MemStore is an in-memory Store: every checkpoint lives only for the life
// of the process. It backs tests and any deployment run without a
// transcripts directory configured.
type MemStore struct {
	mu       sync.RWMutex
	pending  []Turn
	finished map[string]error
}

func NewMemStore() *MemStore {
	return &MemStore{finished: make(map[string]error)}
}

// Checkpoint appends t to the pending list.
func (s *MemStore) Checkpoint(ctx context.Context, t Turn) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, t)
	return nil
}

// Pending returns every checkpointed turn whose run has not been Finish-ed —
// the unresumed state of a crashed run.
func (s *MemStore) Pending(ctx context.Context) ([]Turn, error) {
	if ctx.Err() != nil {
		return []Turn{}, ctx.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var pending []Turn
	for _, t := range s.pending {
		if _, done := s.finished[t.RunID]; !done {
			pending = append(pending, t)
		}
	}
	return pending, nil
}

// Finish marks runID done, so it drops out of Pending. runErr is recorded but
// doesn't change that: a finished run stops being pending whether it
// succeeded or failed.
func (s *MemStore) Finish(ctx context.Context, runID string, runErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished[runID] = runErr
	return nil
}

// DirStore is a Store that appends each Turn as a JSON line to
// <Dir>/<RunID>.jsonl — one file per signal fingerprint, durable across a
// restart. It has no crash-recovery path: Pending always returns nil here,
// unlike MemStore.
type DirStore struct {
	mu  sync.Mutex
	Dir string
}

func NewDirStore(dir string) *DirStore {
	return &DirStore{Dir: dir}
}

// Checkpoint appends t as one JSON line to the run's transcript file.
func (s *DirStore) Checkpoint(ctx context.Context, t Turn) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.appendLine(t.RunID, t)
}

// Pending is unused on this path.
func (s *DirStore) Pending(ctx context.Context) ([]Turn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, nil
}

// Finish appends a terminal record to the run's transcript file noting
// whether it errored.
func (s *DirStore) Finish(ctx context.Context, runID string, runErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	rec := terminalRecord{Finished: true}
	if runErr != nil {
		rec.Error = runErr.Error()
	}
	return s.appendLine(runID, rec)
}

func (s *DirStore) appendLine(runID string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("dir store: marshal: %w", err)
	}
	path := filepath.Join(s.Dir, runID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("dir store: open %s: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("dir store: write %s: %w", path, err)
	}
	return nil
}

type terminalRecord struct {
	Finished bool   `json:"finished"`
	Error    string `json:"error,omitempty"`
}

// Turn is one checkpointed reason-loop step: the run it belongs to, its step
// index, and the full message history up to and including that step.
type Turn struct {
	RunID string
	Step  int
	Msgs  []Message
}

// Message is one turn of the conversation fed to Model.Complete — a role
// (user, assistant, or tool) and its content. There is no structured content
// field: a tool result is folded into Content as a one-line digest, never a
// raw payload.
type Message struct {
	Role    string
	Content string
}

// Completion is one Model.Complete response: the assistant's Message plus
// any tool calls it made in the same turn.
type Completion struct {
	Message   Message
	ToolCalls []ToolCall
}

// ToolCall is one tool invocation the model requested — the tool's name and
// its raw JSON args, decoded by whichever engine branch dispatches that name.
type ToolCall struct {
	Name string
	Args json.RawMessage
}
