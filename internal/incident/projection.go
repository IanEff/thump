package incident

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
var ErrNoFingerprint = errors.New("incident: object carries no fingerprint")

// Projection is the map of fingerprints to Incidents and Records, safe for
// concurrent Apply and Get/Snapshot/GetRecord — Tick's poll loop writes while
// an "incidents" invocation reads.
type Projection struct {
	mu      sync.RWMutex
	records map[string]Record
}

// NewProjection returns an empty Projection, ready for Apply.
func NewProjection() *Projection {
	return &Projection{records: make(map[string]Record)}
}

// Apply folds obj into the Record at its fingerprint via FoldRecord, or returns
// ErrNoFingerprint if obj is none of the four boundary-object types Fold
// recognizes.
func (p *Projection) Apply(obj any) error {
	fp, ok := fingerprintOf(obj)
	if !ok {
		return fmt.Errorf("incident: apply %T: %w", obj, ErrNoFingerprint)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records[fp] = FoldRecord(p.records[fp], obj)
	return nil
}

// Get returns the Incident at fingerprint, or false if Apply has never seen
// that fingerprint.
func (p *Projection) Get(fingerprint string) (Incident, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	rec, ok := p.records[fingerprint]
	if !ok {
		return Incident{}, false
	}
	return rec.Incident, true
}

// GetRecord returns the Record at fingerprint, or false if Apply has never
// seen that fingerprint — carries the raw boundary objects alongside the
// Incident status line.
func (p *Projection) GetRecord(fingerprint string) (Record, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	rec, ok := p.records[fingerprint]
	if !ok {
		return Record{}, false
	}
	return rec, true
}

// Snapshot returns every Incident currently held, in no particular order.
func (p *Projection) Snapshot() []Incident {
	p.mu.RLock()
	defer p.mu.RUnlock()
	incidents := make([]Incident, 0, len(p.records))
	for _, rec := range p.records {
		incidents = append(incidents, rec.Incident)
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
