package clank_test

import (
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
)

func TestRebuildLedger_RecoversAnOpenSetAfterARestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	pub := publish.NewJetPublisher[proposal.Set](js)
	if err := pub.Publish(ctx, "thump.proposals", clickSet()); err != nil {
		t.Fatal(err)
	}

	// simulate a restart: nothing in this process's memory yet, rebuild cold
	ledger, err := clank.RebuildLedgerForTest(ctx, js, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	open, err := ledger.Open(ctx, "slo_burn:ceph-rgw", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("want the pre-restart proposal recovered as open, got %d open sets", len(open))
	}
}

func TestRebuildLedger_ReplaysAnOutcomeOntoItsProposal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	proposals := publish.NewJetPublisher[proposal.Set](js)
	if err := proposals.Publish(ctx, "thump.proposals", clickSet()); err != nil {
		t.Fatal(err)
	}
	outcomes := publish.NewJetPublisher[outcome.Outcome](js)
	if err := outcomes.Publish(ctx, "thump.outcomes", liveSuccess()); err != nil {
		t.Fatal(err)
	}

	ledger, err := clank.RebuildLedgerForTest(ctx, js, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	open, err := ledger.Open(ctx, "slo_burn:ceph-rgw", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("want the outcome to have closed the proposal, got %d still open", len(open))
	}
}

func TestRebuildLedger_ReplaysADeclineOntoItsProposal(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	proposals := publish.NewJetPublisher[proposal.Set](js)
	if err := proposals.Publish(ctx, "thump.proposals", clickSet()); err != nil {
		t.Fatal(err)
	}
	declines := publish.NewJetPublisher[decision.Decision](js)
	dec := decision.Decision{SignalRef: "slo_burn:ceph-rgw", EvaluatedAt: time.Unix(2000, 0)}
	if err := declines.Publish(ctx, "thump.declines", dec); err != nil {
		t.Fatal(err)
	}

	ledger, err := clank.RebuildLedgerForTest(ctx, js, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	open, err := ledger.Open(ctx, "slo_burn:ceph-rgw", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("want the decline to have closed the dedup window, got %d still open", len(open))
	}
}

func TestRebuildLedger_ASupersededProposalStaysClosedNotBothOpen(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	proposals := publish.NewJetPublisher[proposal.Set](js)
	outcomes := publish.NewJetPublisher[outcome.Outcome](js)

	// first proposal, closed by a live outcome
	if err := proposals.Publish(ctx, "thump.proposals", clickSet()); err != nil {
		t.Fatal(err)
	}
	if err := outcomes.Publish(ctx, "thump.outcomes", liveSuccess()); err != nil {
		t.Fatal(err)
	}
	// a second proposal for the same fingerprint, still open
	second := clickSet()
	second.Name = "ps-ceph-rgw-002"
	if err := proposals.Publish(ctx, "thump.proposals", second); err != nil {
		t.Fatal(err)
	}

	ledger, err := clank.RebuildLedgerForTest(ctx, js, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}

	open, err := ledger.Open(ctx, "slo_burn:ceph-rgw", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("want exactly the second proposal open (the outcome must not close it too), got %d open", len(open))
	}
	if open[0].Name != "ps-ceph-rgw-002" {
		t.Errorf("want the SECOND proposal open, got %q", open[0].Name)
	}
}

func TestRebuildLedger_SkipsAnUndecodableMessage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	if _, err := js.Publish(ctx, "thump.proposals", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	proposals := publish.NewJetPublisher[proposal.Set](js)
	if err := proposals.Publish(ctx, "thump.proposals", clickSet()); err != nil {
		t.Fatal(err)
	}

	ledger, err := clank.RebuildLedgerForTest(ctx, js, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	open, err := ledger.Open(ctx, "slo_burn:ceph-rgw", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("want the poison message skipped and the good one recovered, got %d open", len(open))
	}
}
