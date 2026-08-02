package rattle

import "time"

// SignalContract is the freshness/attenuation gate every Reconcile pass runs
// a window through before scoring it — attenuate, don't suppress: stale data
// or a known-bad exclusion window lowers confidence toward ConfidenceFloor
// rather than dropping the detection outright.
type SignalContract struct {
	FreshnessBound       time.Duration     // Fresh rejects a window whose newest sample is older than this
	MinObservationWindow int               // reserved for a minimum-sample-count gate — Fresh does not consult it yet
	ConfidenceFloor      float64           // Attenuated never returns below this, no matter how many exclusion windows overlap
	ExclusionWindows     []ExclusionWindow // known-bad periods that halve confidence when at falls inside one
}

// Attenuated halves base once if at falls inside any ExclusionWindow, then
// floors the result at ConfidenceFloor — at most one penalty applies
// regardless of how many windows overlap, and the result never drops below
// the floor.
func (c SignalContract) Attenuated(base float64, at time.Time) float64 {
	attenuated := base
	for _, ex := range c.ExclusionWindows {
		if !at.Before(ex.Start) && at.Before(ex.End) {
			attenuated = base / 2
			break
		}
	}
	if attenuated < c.ConfidenceFloor {
		return c.ConfidenceFloor
	}
	return attenuated
}

// Fresh reports whether window's newest sample is no older than
// FreshnessBound relative to now. An empty window is never fresh.
func (c SignalContract) Fresh(window []Sample, now time.Time) bool {
	if len(window) == 0 {
		return false
	}
	newest := window[len(window)-1].T
	return now.Sub(newest) <= c.FreshnessBound
}

// ExclusionWindow is one known-bad period (maintenance, a known outage) that
// Attenuated halves confidence for — Reason records why, for the audit trail.
type ExclusionWindow struct {
	Start, End time.Time
	Reason     string
}
