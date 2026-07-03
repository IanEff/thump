package clank

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ianeff/thump/internal/outcome"
	"github.com/ianeff/thump/internal/proposal"
)

type MemProposalLog struct {
	mu   sync.RWMutex
	sets []recorded
}

func NewMemProposalLog() *MemProposalLog {
	return &MemProposalLog{}
}

var ErrNoOpenSet = errors.New("click: no open proposal set answers to this outcome")

func (l *MemProposalLog) Record(ctx context.Context, ps ProposalSet) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sets = append(l.sets, recorded{set: ps, at: time.Now()})
	return nil
}

func (l *MemProposalLog) Open(ctx context.Context, fingerprint string, since time.Time) ([]ProposalSet, error) {
	if ctx.Err() != nil {
		return []ProposalSet{}, ctx.Err()
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	var open []ProposalSet
	for _, r := range l.sets {
		if r.set.SignalRef == fingerprint && r.at.After(since) && isOpen(r.set.Status.Phase) {
			open = append(open, r.set)
		}
	}
	return open, nil
}

func (l *MemProposalLog) Observe(ctx context.Context, o outcome.Outcome) (ProposalSet, error) {
	if ctx.Err() != nil {
		return ProposalSet{}, ctx.Err()
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
	return ProposalSet{}, fmt.Errorf("%w: %s", ErrNoOpenSet, o.SignalRef)
}

func transition(st ProposalStatus, o outcome.Outcome) ProposalStatus {
	st.ObservedAt = o.ExecutedAt
	switch o.Result {
	case outcome.ResultRendered:
		st.Phase = proposal.PhaseAcknowledge // rehearsed, not acted; stays open, keeps deduping
	case outcome.ResultSuccess, outcome.ResultFailure:
		st.Phase, st.Outcome = proposal.PhaseClosed, string(o.Result)
	default: // unknown, partial_non_converging — acted, unsettled, in-flight (the convergence watcher is PARKED)
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
	set ProposalSet
	at  time.Time
}
