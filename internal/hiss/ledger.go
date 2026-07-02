package hiss

import (
	"sync"
	"time"
)

// DecisionLog is hiss' append-only ledger.
type DecisionLog struct {
	mu        sync.RWMutex
	decisions []Decision
}

func NewDecisionLog() *DecisionLog {
	return &DecisionLog{}
}

func (l *DecisionLog) Record(d Decision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.decisions = append(l.decisions, d)
}

func (l *DecisionLog) ByVerdict(v Verdict) []Decision {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Decision
	for _, d := range l.decisions {
		if d.Verdict == v {
			out = append(out, d)
		}
	}
	return out
}

func (l *DecisionLog) Since(cut time.Time) []Decision {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []Decision
	for _, d := range l.decisions {
		if d.EvaluatedAt.After(cut) {
			out = append(out, d)
		}
	}
	return out
}
