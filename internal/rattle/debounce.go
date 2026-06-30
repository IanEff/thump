package rattle

import (
	"time"
)

type Debouncer struct {
	Hold time.Duration
	last map[string]time.Time
}

func NewDebouncer(hold time.Duration) *Debouncer {
	return &Debouncer{Hold: hold, last: make(map[string]time.Time)}
}

func (d *Debouncer) Allow(fingerprint string, now time.Time) bool {
	last, seen := d.last[fingerprint]
	if seen && now.Sub(last) < d.Hold {
		return false // inside the hold window; do not restamp
	}
	d.last[fingerprint] = now
	return true
}
