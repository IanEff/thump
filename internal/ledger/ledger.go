// Package ledger is the append-only, in-memory event log shared by the beats
// that keep one: hiss records every Decision it reaches, thump every Outcome it
// produces. Each beat wraps Log in a named type and adds its own typed query;
// the generic holds the concurrency-safe append plus the two shapes every such
// log needs — Since (a time window) and Filter (a predicate view).
package ledger

import (
	"sync"
	"time"
)

// Log is a concurrency-safe append-only log of T. ts extracts a record's event
// time, which is all Since needs to window the log.
type Log[T any] struct {
	mu    sync.RWMutex
	items []T
	ts    func(T) time.Time
}

// NewLog builds a Log whose Since windows records by the time ts reports for
// each one.
func NewLog[T any](ts func(T) time.Time) *Log[T] {
	return &Log[T]{ts: ts}
}

func (l *Log[T]) Record(v T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, v)
}

// Since returns every record whose event time is after cut, in append order.
func (l *Log[T]) Since(cut time.Time) []T {
	return l.Filter(func(v T) bool { return l.ts(v).After(cut) })
}

// Filter returns every record matching pred, in append order — the primitive a
// beat's typed query (ByVerdict, ByResult, …) is written in terms of.
func (l *Log[T]) Filter(pred func(T) bool) []T {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []T
	for _, v := range l.items {
		if pred(v) {
			out = append(out, v)
		}
	}
	return out
}
