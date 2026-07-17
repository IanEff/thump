package clank

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

var ErrNoOpenSet = errors.New("click: no open proposal set answers to this outcome")

const ledgerRetention = 24 * time.Hour // closed sets older than this are pruned on the next Record; open sets are never pruned, however old

// MemProposalLog is the in-memory ProposalLog and outcome ledger: every
// proposal.Set Propose forms is Record-ed here, gated or not — the set is
// the audit unit, not just the recommendation — and Observe closes the loop
// when an outcome arrives on the click return edge.
type MemProposalLog struct {
	mu   sync.RWMutex
	sets []recorded
}

func NewMemProposalLog() *MemProposalLog {
	return &MemProposalLog{}
}

// Record appends ps to the ledger regardless of whether its gate passed.
// Before appending, it prunes any closed set older than ledgerRetention (24h)
// so the ledger doesn't grow without bound across a long-running process —
// open sets are exempt, however old, because an open set still answers dedup
// queries.
func (l *MemProposalLog) Record(ctx context.Context, ps proposal.Set) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-ledgerRetention)

	// In-place filter to prune old CLOSED proposal sets
	var kept int
	for _, r := range l.sets {
		isClosed := r.set.Status != nil && !isOpen(r.set.Status.Phase)
		if r.at.After(cutoff) || !isClosed {
			l.sets[kept] = r
			kept++
		}
	}
	l.sets = l.sets[:kept]

	l.sets = append(l.sets, recorded{set: ps, at: time.Now()})
	return nil
}

// Open returns every recorded set for fingerprint that is still in an open
// phase (proposed, acknowledged, or acted) and was recorded after since —
// the dedup query: a non-empty result means a live set already answers to
// this signal, so Propose suppresses (but still records) a new one rather
// than delivering it.
func (l *MemProposalLog) Open(ctx context.Context, fingerprint string, since time.Time) ([]proposal.Set, error) {
	if ctx.Err() != nil {
		return []proposal.Set{}, ctx.Err()
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	var open []proposal.Set
	for _, r := range l.sets {
		if r.set.SignalRef == fingerprint && r.at.After(since) && isOpen(r.set.Status.Phase) {
			open = append(open, r.set)
		}
	}
	return open, nil
}

// Observe applies o to the most recently recorded open set for the same
// signal, transitioning its Status.Phase, and returns the updated set.
// ErrNoOpenSet means o arrived with nothing open to answer to — an expected
// outcome of the click return edge, not a defect in this ledger.
func (l *MemProposalLog) Observe(ctx context.Context, o outcome.Outcome) (proposal.Set, error) {
	if ctx.Err() != nil {
		return proposal.Set{}, ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.sets) - 1; i >= 0; i-- {
		r := &l.sets[i]
		if r.set.SignalRef != o.SignalRef || r.set.Status == nil || !isOpen(r.set.Status.Phase) {
			continue
		}
		st := transition(*r.set.Status, o)
		r.set.Status = &st
		return r.set, nil
	}
	return proposal.Set{}, fmt.Errorf("%w: %s", ErrNoOpenSet, o.SignalRef)
}

// Decline closes the newest open set for fingerprint the moment governance
// rules against it — the thump.declines edge, never Observe/Outcome, because
// nothing was rendered or executed for the case base to learn from. Only
// Phase and ObservedAt move; Status.Outcome stays empty, same as it does for
// every other non-terminal-by-execution phase.
func (l *MemProposalLog) Decline(ctx context.Context, fingerprint string, at time.Time) (proposal.Set, error) {
	if ctx.Err() != nil {
		return proposal.Set{}, ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.sets) - 1; i >= 0; i-- {
		r := &l.sets[i]
		if r.set.SignalRef != fingerprint || r.set.Status == nil || !isOpen(r.set.Status.Phase) {
			continue
		}
		st := *r.set.Status
		st.Phase, st.ObservedAt = proposal.PhaseDeclined, at
		r.set.Status = &st
		return r.set, nil
	}
	return proposal.Set{}, fmt.Errorf("%w: %s", ErrNoOpenSet, fingerprint)
}

func transition(st proposal.Status, o outcome.Outcome) proposal.Status {
	st.ObservedAt = o.ExecutedAt
	switch o.Result {
	case outcome.ResultRendered:
		st.Phase = proposal.PhaseAcknowledge // rehearsed, not acted; stays open, keeps deduping
	case outcome.ResultApplied:
		st.Phase, st.Outcome = proposal.PhaseActed, string(o.Result) // acted, awaiting convergence
	case outcome.ResultSuccess, outcome.ResultFailure, outcome.ResultPartialNonConverging:
		st.Phase, st.Outcome = proposal.PhaseClosed, string(o.Result)
	default: // unknown — acted, unsettled
		st.Phase, st.Outcome = proposal.PhaseActed, string(o.Result)
	}
	return st
}

func isOpen(phase string) bool {
	switch phase {
	case proposal.PhaseProposed, proposal.PhaseAcknowledge, proposal.PhaseActed:
		return true // in-flight
	default:
		return false
	}
}

type recorded struct {
	set proposal.Set
	at  time.Time
}
