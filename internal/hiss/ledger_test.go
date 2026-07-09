package hiss_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/hiss"
)

func TestDecisionLog_TheNoPileIsQueryable(t *testing.T) {
	t.Parallel()
	log := hiss.NewDecisionLog()

	approved := goldenDecision()
	escalated := goldenDecision()
	escalated.ID, escalated.Verdict, escalated.GrantedBand = "dec:esc", decision.VerdictEscalate, ""
	escalated.Reasons = []string{hiss.ReasonConfidenceFloor}
	escalated.EvaluatedAt = frozenNow().Add(time.Minute)
	rejected := goldenDecision()
	rejected.ID, rejected.Verdict, rejected.GrantedBand = "dec:rej", decision.VerdictRejected, ""
	rejected.Reasons = []string{hiss.ReasonUngatedInput}
	rejected.EvaluatedAt = frozenNow().Add(2 * time.Minute)

	for _, d := range []decision.Decision{approved, escalated, rejected} {
		log.Record(d)
	}

	if diff := cmp.Diff([]decision.Decision{escalated}, log.ByVerdict(decision.VerdictEscalate)); diff != "" {
		t.Errorf("the escalation pile answered wrong: %v", diff)
	}
	if diff := cmp.Diff([]decision.Decision{rejected}, log.ByVerdict(decision.VerdictRejected)); diff != "" {
		t.Errorf("the rejection pile answered wrong: %v", diff)
	}
	if diff := cmp.Diff([]decision.Decision{escalated, rejected}, log.Since(frozenNow())); diff != "" {
		t.Errorf("Since must return decisions structly after the cut: %v", diff)
	}
}
