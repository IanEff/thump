package thump

import (
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/ledger"
)

// OutcomeLog is thump's append-only ledger of what it rendered — the generic
// ledger.Log plus one thump-specific query.
type OutcomeLog struct {
	*ledger.Log[outcome.Outcome]
}

// NewOutcomeLog returns an empty OutcomeLog, windowed on ExecutedAt.
func NewOutcomeLog() *OutcomeLog {
	return &OutcomeLog{Log: ledger.NewLog(func(o outcome.Outcome) time.Time { return o.ExecutedAt })}
}

// ByResult returns every recorded Outcome whose Result is r — e.g. every
// outcome.ResultRendered dry run, for an operator checking what thump would
// have done.
func (l *OutcomeLog) ByResult(r outcome.Result) []outcome.Outcome {
	return l.Filter(func(o outcome.Outcome) bool { return o.Result == r })
}
