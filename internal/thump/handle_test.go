package thump_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

type fakeOrderPub struct{ published []thump.Order }

func (f *fakeOrderPub) Publish(_ context.Context, _ string, o thump.Order) error {
	f.published = append(f.published, o)
	return nil
}

type fakeOutcomePub struct{ published []outcome.Outcome }

func (f *fakeOutcomePub) Publish(_ context.Context, _ string, o outcome.Outcome) error {
	f.published = append(f.published, o)
	return nil
}

type fakeDeclinePub struct{ published []decision.Decision }

func (f *fakeDeclinePub) Publish(_ context.Context, _ string, d decision.Decision) error {
	f.published = append(f.published, d)
	return nil
}

// TestHandle_RendersExecutesAndPublishesOneOrderAndOutcome pins handle as
// the transport-independent core: no inbox, no glob, no file I/O — just
// Render + Execute + Publish + Record. Tick and (once wired) a NATS handler
// both call this one method; if this test is green, both feeders are green.
func TestHandle_RendersExecutesAndPublishesOneOrderAndOutcome(t *testing.T) {
	t.Parallel()
	orders, outcomes, declines := &fakeOrderPub{}, &fakeOutcomePub{}, &fakeDeclinePub{}
	tr := &thump.Transport{
		OrderPub:   orders,
		OutcomePub: outcomes,
		DeclinePub: declines,
		Catalog:    richCatalog(),
		Log:        thump.NewOutcomeLog(),
		Exec:       thump.DryRun{},
		Now:        frozenNow,
	}

	if err := tr.HandleForTest(context.Background(), approvedGoverned(), nil); err != nil {
		t.Fatal(err)
	}

	if len(orders.published) != 1 {
		t.Fatalf("want exactly one published order, got %d", len(orders.published))
	}
	if diff := cmp.Diff(goldenOrder(), orders.published[0]); diff != "" {
		t.Error("order drifted from the golden fixture (-want +got)", diff)
	}
	if len(outcomes.published) != 1 {
		t.Fatalf("want exactly one published outcome, got %d", len(outcomes.published))
	}
	if diff := cmp.Diff(goldenOutcome(), outcomes.published[0]); diff != "" {
		t.Error("outcome drifted from the golden fixture (-want +got)", diff)
	}
	if got := len(tr.Log.ByResult(outcome.ResultRendered)); got != 1 {
		t.Errorf("one handle call must mean one ledger record, got %d", got)
	}
	if len(declines.published) != 0 {
		t.Errorf("an approval must publish no decline notice, got %+v", declines.published)
	}
}

// TestHandle_NonApprovalIsANoOp is the other half: a valid non-approval must
// render nothing and execute nothing — the same "success, not failure"
// distinction the guide's Ack/error contract depends on over the broker.
// It DOES publish a decline notice — that's the one job the branch has now.
func TestHandle_NonApprovalIsANoOp(t *testing.T) {
	t.Parallel()
	orders, outcomes, declines := &fakeOrderPub{}, &fakeOutcomePub{}, &fakeDeclinePub{}
	tr := &thump.Transport{
		OrderPub:   orders,
		OutcomePub: outcomes,
		DeclinePub: declines,
		Catalog:    richCatalog(),
		Log:        thump.NewOutcomeLog(),
		Exec:       thump.DryRun{},
		Now:        frozenNow,
	}

	if err := tr.HandleForTest(context.Background(), escalatedGoverned(), nil); err != nil {
		t.Fatal(err)
	}
	if len(orders.published) != 0 || len(outcomes.published) != 0 {
		t.Errorf("an escalated decision must render and execute nothing: orders=%+v outcomes=%+v",
			orders.published, outcomes.published)
	}
	if diff := cmp.Diff([]decision.Decision{escalatedGoverned().Decision}, declines.published); diff != "" {
		t.Error("decline notice drifted from the golden fixture (-want +got)", diff)
	}
}

// TestHandle_NonApprovalLogsContractRef closes E4's thump gap: a non-approved
// run — the governance-correct, expected outcome for most confidence-floor
// escalations, not a rare edge case — was previously undiagnosable from
// kubectl logs alone, because the outcome line only carried contractRef on
// the render/acted branch. escalatedGoverned's Set carries its Recommended
// Candidate's ContractRef untouched by the escalation, so it must show up
// here even though nothing was rendered or executed.
func TestHandle_NonApprovalLogsContractRef(t *testing.T) {
	getLogs := captureLog(t)
	tr := &thump.Transport{
		OrderPub:   &fakeOrderPub{},
		OutcomePub: &fakeOutcomePub{},
		DeclinePub: &fakeDeclinePub{},
		Catalog:    richCatalog(),
		Log:        thump.NewOutcomeLog(),
		Exec:       thump.DryRun{},
		Now:        frozenNow,
	}

	if err := tr.HandleForTest(context.Background(), escalatedGoverned(), nil); err != nil {
		t.Fatal(err)
	}

	line := onlyOutcomeLine(t, getLogs())
	if diff := cmp.Diff("throttle-non-critical-paths", line["contractRef"]); diff != "" {
		t.Error("a non-approval's outcome line must still carry contractRef (-want +got)", diff)
	}
}
