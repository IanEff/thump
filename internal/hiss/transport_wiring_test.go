package hiss_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
)

// TestBuildTransport_ReachesRebuiltHoldsNotAnEmptyMap is hiss's wiring
// guard, the row-5 twin of clank's TestBuildIntake_FullyConfiguredReachesRealChangeSource
// (W0a's inventory, W0d's own follow-up): PendingHolds is restart-lossy
// unless the composition root actually calls rebuildHolds. rebuild_test.go
// already pins rebuildHolds itself works; this pins that buildTransport —
// the function runBroker actually calls — reaches it, rather than a bare
// NewPendingHolds() a future edit could swap in without any other test
// failing.
func TestBuildTransport_ReachesRebuiltHoldsNotAnEmptyMap(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	pub := publish.NewJetPublisher[decision.Governed](js)
	held := decision.Governed{Decision: decision.Decision{SignalRef: "fp-1", Verdict: decision.VerdictHold}}
	if err := pub.Publish(ctx, "thump.decisions", held); err != nil {
		t.Fatal(err)
	}

	tr, err := hiss.BuildTransportForTest(ctx, js, pub, calmPolicy(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := tr.Holds.Take("fp-1")
	if !ok {
		t.Fatal("want a fully-configured broker path to reach the pre-restart hold, got not-found — Holds was seeded empty")
	}
	if diff := cmp.Diff(held, got); diff != "" {
		t.Error("wrong Governed recovered (-want +got)", diff)
	}
}
