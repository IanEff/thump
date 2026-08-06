package calipers

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// ErrNoFingerprint is Apply's error when obj carries no fingerprint —
// fingerprintOf found none of the four boundary-object types, or the field
// it checked was empty. Apply refuses the object rather than folding it in
// under a zero-value key.
var ErrNoFingerprint = errors.New("calipers: object carries no fingerprint")

// Projection is the map of fingerprints to Incidents, safe for concurrent
// Apply and Get/Snapshot — Tick's poll loop writes while an "incidents"
// invocation reads.
type Projection struct {
	mu        sync.RWMutex
	incidents map[string]Incident
}

// NewProjection returns an empty Projection, ready for Apply.
func NewProjection() *Projection {
	return &Projection{incidents: make(map[string]Incident)}
}

// Apply folds obj into the Incident at its fingerprint via Fold, or returns
// ErrNoFingerprint if obj is none of the four boundary-object types Fold
// recognizes.
func (p *Projection) Apply(obj any) error {
	fp, ok := fingerprintOf(obj)
	if !ok {
		return fmt.Errorf("calipers: apply %T: %w", obj, ErrNoFingerprint)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.incidents[fp] = Fold(p.incidents[fp], obj)
	return nil
}

// Get returns the Incident at fingerprint, or false if Apply has never seen
// that fingerprint.
func (p *Projection) Get(fingerprint string) (Incident, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	incident, ok := p.incidents[fingerprint]
	if !ok {
		return Incident{}, false
	}
	return incident, true
}

// Snapshot returns every Incident currently held, in no particular order.
func (p *Projection) Snapshot() []Incident {
	p.mu.RLock()
	defer p.mu.RUnlock()
	incidents := make([]Incident, 0, len(p.incidents))
	for _, incident := range p.incidents {
		incidents = append(incidents, incident)
	}
	return incidents
}

func fingerprintOf(obj any) (string, bool) {
	switch v := obj.(type) {
	case signal.Detection:
		return v.Fingerprint, v.Fingerprint != ""
	case proposal.Set:
		return v.SignalRef, v.SignalRef != ""
	case decision.Governed:
		return v.Decision.SignalRef, v.Decision.SignalRef != ""
	case outcome.Outcome:
		return v.SignalRef, v.SignalRef != ""
	default:
		return "", false
	}
}
