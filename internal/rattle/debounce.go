package rattle

import (
	"time"
)

// Debouncer suppresses a repeat detection for the same fingerprint inside a
// hold window — Reconcile calls Allow once per firing detector, so debounce
// state is per fingerprint, not per SLO or per detector type.
type Debouncer struct {
	Hold time.Duration
	last map[string]time.Time
}

// NewDebouncer returns a Debouncer with an empty history and the given hold window.
func NewDebouncer(hold time.Duration) *Debouncer {
	return &Debouncer{Hold: hold, last: make(map[string]time.Time)}
}

// Allow reports whether fingerprint may fire again at now: false, without
// updating any state, if now is inside Hold since the last allowed call;
// true, restamping the last-seen time, otherwise. A suppressed call never
// restamps — the hold window is measured from the last permitted signal, not
// the last suppressed one, so a continuously-firing detector still gets
// exactly one signal per Hold interval.
func (d *Debouncer) Allow(fingerprint string, now time.Time) bool {
	last, seen := d.last[fingerprint]
	if seen && now.Sub(last) < d.Hold {
		return false // inside the hold window; do not restamp
	}
	d.last[fingerprint] = now
	return true
}
