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

// NewDecisionLog returns an empty DecisionLog, windowed on EvaluatedAt.
func NewDecisionLog() *DecisionLog {
	return &DecisionLog{Log: ledger.NewLog(func(d decision.Decision) time.Time { return d.EvaluatedAt })}
}

// ByVerdict returns every recorded Decision whose Verdict is v — e.g. every
// escalation hiss has raised, for an operator triaging what needs a human.
func (l *DecisionLog) ByVerdict(v decision.Verdict) []decision.Decision {
	return l.Filter(func(d decision.Decision) bool { return d.Verdict == v })
}
