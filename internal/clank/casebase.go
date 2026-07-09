package clank

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

const maxCases = 10000 // CaseBase caps here; Append evicts the oldest case first once full

// Case is one closed loop of case-base learning: a proposal, the decision
// that governed it, and the outcome it produced, joined by their shared
// fingerprint. It exists to compute Alignment — bookkeeping for the scorer's
// Prior, not a belief in its own right; the proposal.Set stays the audit unit.
type Case struct {
	Fingerprint  string
	DecisionRef  string
	OutcomeRef   string
	ContractRef  string
	FailureClass proposal.FailureClass
	Confidence   float64
	Mode         outcome.Mode
	Result       outcome.Result
	ObservedAt   time.Time
}

const minCorroboration = 2 // belief-formation defence 1: fewer than 2 live votes and Alignment reports no prior, not a weak one

var ErrUnprovenancedCase = errors.New("click: a case that can't be traced is poison")

// CaseBase is the read side of the Learn edge: a bounded, concurrency-safe
// list of Cases that CausalScorerImpl reads through the Prior interface.
// Nothing here writes proposal.Set.Status.Outcome or calibrates confidence
// directly — only Alignment feeds back into scoring, and only once
// corroborated.
type CaseBase struct {
	mu    sync.RWMutex
	cases []Case
}

func NewCaseBase() *CaseBase {
	return &CaseBase{}
}

// Append records c, evicting the oldest case first once the base holds
// maxCases (10000) — bounded so a long-running process doesn't grow it
// forever. A Case missing its fingerprint, outcome ref, decision ref, or
// result is rejected as ErrUnprovenancedCase: a case that can't be traced
// back to a decision can't be trusted as a corroborating vote.
func (cb *CaseBase) Append(c Case) error {
	if c.Fingerprint == "" || c.OutcomeRef == "" || c.DecisionRef == "" || c.Result == "" {
		return fmt.Errorf("%w: %v", ErrUnprovenancedCase, c)
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if len(cb.cases) >= maxCases {
		// Shift everything left by 1 to drop index 0
		copy(cb.cases, cb.cases[1:])
		cb.cases[len(cb.cases)-1] = c
	} else {
		cb.cases = append(cb.cases, c)
	}
	return nil
}

// Cases returns every recorded Case for fingerprint, in insertion order.
func (cb *CaseBase) Cases(fingerprint string) []Case {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	var out []Case
	for _, c := range cb.cases {
		if c.Fingerprint == fingerprint {
			out = append(out, c)
		}
	}
	return out
}

// Len reports how many cases have been learned — a test seam for asserting
// the Learn edge actually absorbed an outcome, not just that it didn't error.
func (cb *CaseBase) Len() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return len(cb.cases)
}

// Alignment implements Prior: the fraction of past live outcomes for
// fingerprint that succeeded. It returns ok=false — no prior, not a weak
// one — until at least minCorroboration (2) live votes exist, the ≥2-source
// corroboration floor (defence 1). Dry-run and rendered outcomes never
// vote; only outcome.ModeLive results count toward the tally.
func (cb *CaseBase) Alignment(fingerprint string) (float64, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	var votes, successes int
	for _, c := range cb.cases {
		if c.Fingerprint != fingerprint || c.Mode != outcome.ModeLive {
			continue
		}
		switch c.Result {
		case outcome.ResultSuccess:
			votes, successes = votes+1, successes+1
		case outcome.ResultFailure, outcome.ResultPartialNonConverging:
			votes++
		}
	}
	if votes < minCorroboration {
		return 0, false
	}
	return float64(successes) / float64(votes), true
}
