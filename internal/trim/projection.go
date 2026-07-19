package trim

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

var ErrNoFingerprint = errors.New("trim: object carries no fingerprint")

// Projection is the map of fingerprints to Incidents.
type Projection struct {
	mu        sync.RWMutex
	incidents map[string]Incident
}

func NewProjection() *Projection {
	return &Projection{incidents: make(map[string]Incident)}
}

func (p *Projection) Apply(obj any) error {
	fp, ok := fingerprintOf(obj)
	if !ok {
		return fmt.Errorf("trim: apply %T: %w", obj, ErrNoFingerprint)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.incidents[fp] = Fold(p.incidents[fp], obj)
	return nil
}

func (p *Projection) Get(fingerprint string) (Incident, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	incident, ok := p.incidents[fingerprint]
	if !ok {
		return Incident{}, false
	}
	return incident, true
}

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
