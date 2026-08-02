package clank

import (
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
		if cs.FailureClass != class || cs.Result == outcome.ResultRendered {
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

	var cases []Case
	for _, o := range outcomes {
		set, ok := latestSetBefore(byFingerprint[o.SignalRef], o.ExecutedAt)
		if !ok {
			continue
		}
		cases = append(cases, newCase(set, o))
	}
	return cases
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
