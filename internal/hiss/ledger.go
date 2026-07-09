package hiss

import (
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/ledger"
)

// DecisionLog is hiss' append-only ledger of the verdicts it reached — the
// generic ledger.Log plus one hiss-specific query.
type DecisionLog struct {
	*ledger.Log[decision.Decision]
}

func NewDecisionLog() *DecisionLog {
	return &DecisionLog{Log: ledger.NewLog(func(d decision.Decision) time.Time { return d.EvaluatedAt })}
}

func (l *DecisionLog) ByVerdict(v decision.Verdict) []decision.Decision {
	return l.Filter(func(d decision.Decision) bool { return d.Verdict == v })
}
