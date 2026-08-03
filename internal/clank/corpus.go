package clank

import (
	"slices"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

// Corpus is the calibration record: every closed loop the
// engine has emitted, joined from a shaped WAL rather than
// computed.
type Corpus struct {
	Cases    []Case
	MinedAt  time.Time
	Segments []string
}

func (c Corpus) FloorSupport(class proposal.FailureClass, floor float64) FloorSupport {
	fs := FloorSupport{Class: class, Floor: floor}
	for _, cs := range c.Cases {
		if cs.FailureClass != class || cs.Result == outcome.ResultRendered || cs.Result == outcome.ResultApplied {
			continue
		}
		win := cs.Result == outcome.ResultSuccess
		if cs.Confidence >= floor {
			fs.AdmittedTotal++
			if win {
				fs.AdmittedWins++
			}
			continue
		}
		fs.RefusedTotal++
		if win {
			fs.RefusedWins++
		}
	}
	return fs
}

// FloorSupport reports, for one failure class, how the observed
// outcomes distribute either side of a candidate floor.
type FloorSupport struct {
	Class         proposal.FailureClass
	Floor         float64
	AdmittedTotal int // cases with Confidence >= Floor
	AdmittedWins  int // of those, Result == ResultSuccess
	RefusedTotal  int // cases below floor
	RefusedWins   int // of those, Result == ResultSuccess
}

// MineCorpus joins sets and outcomes by SignalRef, mirroring
// MemProposalLog.Observe's live match: an outcome pairs with
// the most recent set sharing its SignalRef that had already
// reached a terminal Status by the outcome's ExecutedAt.
func MineCorpus(sets []proposal.Set, outcomes []outcome.Outcome) []Case {
	byFingerprint := make(map[string][]proposal.Set)
	for _, s := range sets {
		byFingerprint[s.SignalRef] = append(byFingerprint[s.SignalRef], s)
	}

	byIncident := make(map[incidentKey][]outcome.Outcome)
	for _, o := range outcomes {
		key := incidentKey{o.SignalRef, o.DecisionRef}
		byIncident[key] = append(byIncident[key], o)
	}

	var cases []Case
	for _, os := range byIncident {
		terminal, ok := terminalOutcome(os)
		if !ok {
			continue
		}

		set, ok := latestSetBefore(byFingerprint[terminal.SignalRef], terminal.ExecutedAt)
		if !ok {
			continue
		}
		cases = append(cases, newCase(set, terminal))
	}

	slices.SortFunc(cases, func(a, b Case) int { return a.ObservedAt.Compare(b.ObservedAt) })
	return cases
}

type incidentKey struct {
	signalRef   string
	decisionRef string
}

// terminalOutcome picks the settled outcome out of every record publised for
// one incident, skipping ResultApplied, and preferring the latest ExecutedAt if
// more than one settled record exists.
func terminalOutcome(outcomes []outcome.Outcome) (outcome.Outcome, bool) {
	var best outcome.Outcome
	var found bool
	for _, o := range outcomes {
		if o.Result == outcome.ResultApplied {
			continue
		}
		if !found || o.ExecutedAt.After(best.ExecutedAt) {
			best = o
			found = true
		}
	}
	return best, found
}

func latestSetBefore(sets []proposal.Set, at time.Time) (proposal.Set, bool) {
	var best proposal.Set
	var found bool
	for _, s := range sets {
		if s.Status == nil || s.Status.ObservedAt.After(at) {
			continue
		}
		if !found || s.Status.ObservedAt.After(best.Status.ObservedAt) {
			best = s
			found = true
		}
	}
	return best, found
}
