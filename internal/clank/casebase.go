package clank

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

const maxCases = 10000

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

const minCorroboration = 2

var ErrUnprovenancedCase = errors.New("click: a case that can't be traced is poison")

type CaseBase struct {
	mu    sync.RWMutex
	cases []Case
}

func NewCaseBase() *CaseBase {
	return &CaseBase{}
}

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
