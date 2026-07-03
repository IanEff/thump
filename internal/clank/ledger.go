package clank

import (
	"context"
	"errors"
	"sync"
	"time"
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

func isOpen(phase string) bool {
	switch phase {
	case "proposed", "acknowledge", "acted":
		return true // in-flight
	default:
		return false
	}
}

type recorded struct {
	set ProposalSet
	at  time.Time
}
