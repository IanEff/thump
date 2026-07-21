package clank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Store is the reason loop's checkpoint memory — one Turn per model
// completion, not the proposal ledger (MemProposalLog): a different lifetime
// and a different granularity. Checkpoint must succeed before the loop takes
// its next turn; a checkpoint error halts the run rather than risk a turn
// nothing remembers. Because Propose never mutates infrastructure,
// re-running a halted signal from scratch is always safe — Pending exists
// as the seam a future crash-recovery path would read from, but nothing in
// Main wires it up today, deliberately: restarting is the documented
// safety property, not resuming a stale conversation.
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
// <Dir>/<RunID>.jsonl — one file per run (engine.go keys RunID
// fingerprint/unixnano, so two runs of the same signal never share a file),
// durable across a restart. Pending always returns nil here, unlike
// MemStore — on purpose, not an oversight; see the Store doc comment.
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

// Pending always returns nil — see the Store doc comment.
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
	// runID may itself contain "/" (engine.go keys it fingerprint/unixnano,
	// not the bare fingerprint), so its parent directory won't exist yet on
	// this run's first write.
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("dir store: mkdir %s: %w", filepath.Dir(path), err)
	}
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

var _ Store = (*S3Store)(nil)

// S3Store is a Store that writes each checkpointed Turn as its own object,
// keyed by run and step (transcripts/<RunID>/<Step>.json, RunID always
// fingerprint/unixnano — see engine.go) — never a read-modify-write against
// a shared key, unlike DirStore's shared per-run file. That's why it
// carries no mutex: the AWS SDK's S3 client is already safe for concurrent
// use, and two Checkpoint calls never target the same object key, even for
// two runs of the same signal. Pending always returns nil — see the Store
// doc comment.
type S3Store struct {
	Client *s3.Client
	Bucket string
}

// NewS3Store returns an S3Store writing objects to bucket via client.
func NewS3Store(client *s3.Client, bucket string) *S3Store {
	return &S3Store{Client: client, Bucket: bucket}
}

// Checkpoint puts t as its own object at transcripts/<RunID>/<Step>.json.
func (s *S3Store) Checkpoint(ctx context.Context, t Turn) error {
	return s.putJSON(ctx, turnKey(t.RunID, t.Step), t)
}

// Pending always returns nil, same as DirStore.
func (s *S3Store) Pending(ctx context.Context) ([]Turn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, nil
}

// Finish puts a terminal record at transcripts/<RunID>/finish.json noting
// whether the run errored.
func (s *S3Store) Finish(ctx context.Context, runID string, runErr error) error {
	rec := terminalRecord{Finished: true}
	if runErr != nil {
		rec.Error = runErr.Error()
	}
	return s.putJSON(ctx, finishKey(runID), rec)
}

func (s *S3Store) putJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("s3 store: marshal: %w", err)
	}
	_, err = s.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(b),
	})
	if err != nil {
		return fmt.Errorf("s3 store: put %s: %w", key, err)
	}
	return nil
}

func turnKey(runID string, step int) string {
	return fmt.Sprintf("transcripts/%s/%d.json", runID, step)
}

func finishKey(runID string) string {
	return fmt.Sprintf("transcripts/%s/finish.json", runID)
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
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
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
