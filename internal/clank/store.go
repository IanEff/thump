package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store interface {
	Checkpoint(context.Context, Turn) error
	Pending(context.Context) ([]Turn, error)
	Finish(context.Context, string, error) error
}

type MemStore struct {
	mu       sync.RWMutex
	pending  []Turn
	finished map[string]error
}

func NewMemStore() *MemStore {
	return &MemStore{finished: make(map[string]error)}
}

func (s *MemStore) Checkpoint(ctx context.Context, t Turn) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, t)
	return nil
}

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

func (s *MemStore) Finish(ctx context.Context, runID string, runErr error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished[runID] = runErr
	return nil
}

type DirStore struct {
	mu  sync.Mutex
	Dir string
}

func NewDirStore(dir string) *DirStore {
	return &DirStore{Dir: dir}
}

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

type Turn struct {
	RunID string
	Step  int
	Msgs  []Message
}

type Message struct {
	Role    string
	Content string
}

type Completion struct {
	Message   Message
	ToolCalls []ToolCall
}

type ToolCall struct {
	Name string
	Args json.RawMessage
}
