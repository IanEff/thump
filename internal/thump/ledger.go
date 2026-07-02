package thump

import (
	"sync"
	"time"
)

// OutcomeLog is thump's append-only ledger.
type OutcomeLog struct {
	mu       sync.RWMutex
	outcomes []Outcome
}

func NewOutcomeLog() *OutcomeLog {
	return &OutcomeLog{}
}

func (l *OutcomeLog) Record(o Outcome) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.outcomes = append(l.outcomes, o)
}

func (l *OutcomeLog) ByResult(r Result) []Outcome {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Outcome
	for _, o := range l.outcomes {
		if o.Result == r {
			out = append(out, o)
		}
	}
	return out
}

func (l *OutcomeLog) Since(cut time.Time) []Outcome {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Outcome
	for _, o := range l.outcomes {
		if o.ExecutedAt.After(cut) {
			out = append(out, o)
		}
	}
	return out
}
