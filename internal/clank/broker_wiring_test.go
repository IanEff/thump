package clank_test

import (
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
)

// TestBuildLedger_ReachesRebuiltHistoryNotAnEmptyLedger is clank's wiring
// guard, the row-6 twin of hiss's TestBuildTransport_ReachesRebuiltHoldsNotAnEmptyMap
// (W0a's inventory): MemProposalLog is restart-lossy unless the composition
// root actually calls rebuildLedger. rebuild_test.go already pins
// rebuildLedger itself works; this pins that buildLedger — the function
// runBroker actually calls — reaches it, rather than a bare
// NewMemProposalLog() a future edit could swap in without any other test
// failing.
func TestBuildLedger_ReachesRebuiltHistoryNotAnEmptyLedger(t *testing.T) {
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

	ledger, err := clank.BuildLedgerForTest(ctx, js, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	open, err := ledger.Open(ctx, "slo_burn:ceph-rgw", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatal("want a fully-configured broker path to reach the pre-restart proposal, got not-found — the ledger was seeded empty")
	}
}
