package thump

import (
	"context"
	"fmt"
	"os"
	"sync"

	"sigs.k8s.io/yaml"
)

// FileSwitch is a KillSwitch backed by a polled config file: Armed answers from
// an in-memory flag that Reload refreshes, so an Execute call never blocks on
// disk. Any doubt reads as disarmed — a missing file, an unreadable one, or
// malformed contents all leave live actuation OFF, and only a well-formed file
// that explicitly says armed: true turns it on. The safe direction for the
// highest-blast-radius beat is to not act, so a failed reload clears a stale
// armed state rather than latching it.
type FileSwitch struct {
	path  string
	mu    sync.RWMutex
	armed bool // guarded by mu; zero value false — a fresh switch is disarmed until the first good Reload
}

// NewFileSwitch returns a switch that reads path on each Reload. It starts
// disarmed; nothing is read until Reload runs.
func NewFileSwitch(path string) *FileSwitch {
	return &FileSwitch{path: path}
}

// Armed reports the last successfully-loaded state without touching disk — a
// slow or hung filesystem can stall Reload, never a live Execute.
func (s *FileSwitch) Armed(context.Context) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.armed
}

// switchConfig is the whole on-disk shape: one bool. The switch is a single
// global trust state, not a policy document, so it stays this small.
type switchConfig struct {
	Armed bool `json:"armed"`
}

// Reload re-reads the file and swaps the in-memory flag, returning an error for
// logging. On any failure it first forces the flag to disarmed — a read it
// can't trust must not leave a previous "armed" latched.
func (s *FileSwitch) Reload(context.Context) error {
	raw, err := os.ReadFile(s.path) //nolint:gosec
	if err != nil {
		s.set(false)
		return fmt.Errorf("kill-switch read: %w", err)
	}
	var cfg switchConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		s.set(false)
		return fmt.Errorf("kill-switch parse: %w", err)
	}
	s.set(cfg.Armed)
	return nil
}

func (s *FileSwitch) set(armed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = armed
}
