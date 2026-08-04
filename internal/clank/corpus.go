package clank

import (
	"slices"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

// CorpusVersion is the artifact layout this build writes — readCorpus
// migrates anything older and refuses anything newer, since a best-effort
// decode of an unknown layout is how a field goes silently empty.
const CorpusVersion = 2

// Corpus is the calibration record: every closed loop the engine has
// emitted, joined from a shaped WAL rather than computed.
type Corpus struct {
	Version  int       `json:"version"`
	Cases    []Case    `json:"cases"`
	MinedAt  time.Time `json:"minedAt"`
	Segments []string  `json:"segments"`
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
// reached a terminal Status by the outcome's ExecutedAt. Every outcome
// record for an incident becomes a candidate Case; CollapseCases then
// reduces each (SignalRef, DecisionRef) group to the one that survives.
func MineCorpus(sets []proposal.Set, outcomes []outcome.Outcome) []Case {
	byFingerprint := make(map[string][]proposal.Set)
	for _, s := range sets {
		byFingerprint[s.SignalRef] = append(byFingerprint[s.SignalRef], s)
	}

	var cases []Case
	for _, o := range outcomes {
		set, ok := latestSetBefore(byFingerprint[o.SignalRef], o.ExecutedAt)
		if !ok {
			continue
		}
		cases = append(cases, newCase(set, o))
	}

	cases = CollapseCases(cases)
	slices.SortFunc(cases, func(a, b Case) int { return a.ObservedAt.Compare(b.ObservedAt) })
	return cases
}

type incidentKey struct {
	signalRef   string
	decisionRef string
}

// CollapseCases reduces cases to one per (SignalRef, DecisionRef) group —
// the settled record, never ResultApplied, preferring the latest ObservedAt
// when more than one settled record exists. ResultApplied is a live
// action's execute-time ack, superseded by whatever the convergence watcher
// later settles; a group that only ever reached ResultApplied is dropped,
// since nothing has settled it yet. MineCorpus and mergeCorpus both call
// this, so a freshly mined batch and an artifact merged across many mining
// runs collapse the same way.
func CollapseCases(cases []Case) []Case {
	var order []incidentKey
	seen := make(map[incidentKey]bool, len(cases))
	best := make(map[incidentKey]Case, len(cases))
	for _, c := range cases {
		key := incidentKey{c.SignalRef, c.DecisionRef}
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
		if c.Result == outcome.ResultApplied {
			continue
		}
		if cur, ok := best[key]; !ok || c.ObservedAt.After(cur.ObservedAt) {
			best[key] = c
		}
	}

	var out []Case
	for _, key := range order {
		if c, ok := best[key]; ok {
			out = append(out, c)
		}
	}
	return out
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
