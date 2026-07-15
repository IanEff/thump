package hiss_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/hiss"
)

type fakeDecisionPub struct{ published []decision.Governed }

func (f *fakeDecisionPub) Publish(_ context.Context, _ string, g decision.Governed) error {
	f.published = append(f.published, g)
	return nil
}

// TestHandle_EvaluatesAndPublishesOneDecision pins handle as the
// transport-independent core: no inbox, no glob, no file I/O — just
// Evaluate + Record + Publish. Tick and (once wired) a NATS handler both
// call this one method; if this test is green, both feeders are green.
func TestHandle_EvaluatesAndPublishesOneDecision(t *testing.T) {
	t.Parallel()
	fake := &fakeDecisionPub{}
	tr := &hiss.Transport{Pub: fake, Policy: calmPolicy(), Log: hiss.NewDecisionLog(), Now: frozenNow}

	if err := tr.HandleForTest(context.Background(), governedSet(), nil); err != nil {
		t.Fatal(err)
	}

	if len(fake.published) != 1 {
		t.Fatalf("want exactly one published decision, got %d", len(fake.published))
	}
	if diff := cmp.Diff(goldenDecision(), fake.published[0].Decision); diff != "" {
		t.Error("decision drifted from the golden fixture (-want +got)", diff)
	}
	if got := len(tr.Log.ByVerdict(decision.VerdictApproved)); got != 1 {
		t.Errorf("one handle call must mean one ledger record, got %d", got)
	}
}

// TestHandle_DecisionLogsContractRef closes E4's hiss gap: the decision line
// carried requestedBand/grantedBand but no contractRef, so which catalogued
// action a verdict concerned was undiagnosable from kubectl logs alone —
// the same gap clank's and thump's log lines had.
func TestHandle_DecisionLogsContractRef(t *testing.T) {
	getLogs := captureLog(t)
	fake := &fakeDecisionPub{}
	tr := &hiss.Transport{Pub: fake, Policy: calmPolicy(), Log: hiss.NewDecisionLog(), Now: frozenNow}

	if err := tr.HandleForTest(context.Background(), governedSet(), nil); err != nil {
		t.Fatal(err)
	}

	line := onlyDecisionLine(t, getLogs())
	if diff := cmp.Diff("throttle-non-critical-paths", line["contractRef"]); diff != "" {
		t.Error("decision line must carry contractRef (-want +got)", diff)
	}
}
