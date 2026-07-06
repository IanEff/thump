package thump_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
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

// TestHandle_RendersExecutesAndPublishesOneOrderAndOutcome pins handle as
// the transport-independent core: no inbox, no glob, no file I/O — just
// Render + Execute + Publish + Record. Tick and (once wired) a NATS handler
// both call this one method; if this test is green, both feeders are green.
func TestHandle_RendersExecutesAndPublishesOneOrderAndOutcome(t *testing.T) {
	t.Parallel()
	orders, outcomes := &fakeOrderPub{}, &fakeOutcomePub{}
	tr := &thump.Transport{
		OrderPub:   orders,
		OutcomePub: outcomes,
		Catalog:    richCatalog(),
		Log:        thump.NewOutcomeLog(),
		Exec:       thump.DryRun{},
		Now:        frozenNow,
	}

	if err := tr.HandleForTest(context.Background(), approvedGoverned()); err != nil {
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
	if got := len(tr.Log.ByResult(thump.ResultRendered)); got != 1 {
		t.Errorf("one handle call must mean one ledger record, got %d", got)
	}
}

// TestHandle_NonApprovalIsANoOp is the other half: a valid non-approval must
// render nothing and publish nothing — the same "success, not failure"
// distinction the guide's Ack/error contract depends on over the broker.
func TestHandle_NonApprovalIsANoOp(t *testing.T) {
	t.Parallel()
	orders, outcomes := &fakeOrderPub{}, &fakeOutcomePub{}
	tr := &thump.Transport{
		OrderPub:   orders,
		OutcomePub: outcomes,
		Catalog:    richCatalog(),
		Log:        thump.NewOutcomeLog(),
		Exec:       thump.DryRun{},
		Now:        frozenNow,
	}

	if err := tr.HandleForTest(context.Background(), escalatedGoverned()); err != nil {
		t.Fatal(err)
	}
	if len(orders.published) != 0 || len(outcomes.published) != 0 {
		t.Errorf("an escalated decision must render and publish nothing: orders=%+v outcomes=%+v",
			orders.published, outcomes.published)
	}
}
