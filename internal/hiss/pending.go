package hiss

import (
	"sync"

	"github.com/ianeff/thump/api/v1/decision"
)

// PendingHolds is hiss's record of Candidates it has held awaiting a human
// ack — keyed by fingerprint, retaining the exact Governed it judged so an
// Approval re-evaluates against that Set, never a reconstructed one. It is
// governance state, so it lives here and nowhere else, and it is rebuildable
// from hiss's own emitted holds — losing it costs at most one re-nag cycle,
// never a wrong verdict.
type PendingHolds struct {
	mu    sync.Mutex
	holds map[string]decision.Governed
}

// NewPendingHolds returns an empty PendingHolds.
func NewPendingHolds() *PendingHolds {
	return &PendingHolds{holds: make(map[string]decision.Governed)}
}

// Record retains g under its own fingerprint — a second Record for the same
// fingerprint overwrites the first, which is correct: clank's dedupe ledger
// keeps a held fingerprint from producing a second open Set, so Record is
// never racing itself in practice.
func (h *PendingHolds) Record(g decision.Governed) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.holds[g.Decision.SignalRef] = g
}

// Take pops and returns the held Governed for fingerprint — the second
// return is false for an unknown fingerprint, so a redelivered or
// already-resolved ack finds nothing and is inert, never an error.
func (h *PendingHolds) Take(fingerprint string) (decision.Governed, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.holds[fingerprint]
	if ok {
		delete(h.holds, fingerprint)
	}
	return g, ok
}
