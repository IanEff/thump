package clank

import (
	"context"
	"encoding/json"
	"sync"
)

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
