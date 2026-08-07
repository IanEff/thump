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

func TestRebuildHolds_RecoversAnOpenHoldAfterARestart(t *testing.T) {
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

	// simulate a restart: nothing in this process's memory yet, rebuild cold
	holds, err := hiss.RebuildHoldsForTest(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := holds.Take("fp-1")
	if !ok {
		t.Fatal("want the pre-restart hold recovered, got not-found")
	}
	if diff := cmp.Diff(held, got); diff != "" {
		t.Error("wrong Governed recovered (-want +got)", diff)
	}
}

func TestRebuildHolds_RecoversAnOpenEscalateAfterARestart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	pub := publish.NewJetPublisher[decision.Governed](js)
	escalated := decision.Governed{Decision: decision.Decision{SignalRef: "fp-1", Verdict: decision.VerdictEscalate}}
	if err := pub.Publish(ctx, "thump.decisions", escalated); err != nil {
		t.Fatal(err)
	}

	// simulate a restart: nothing in this process's memory yet, rebuild cold
	holds, err := hiss.RebuildHoldsForTest(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := holds.Take("fp-1")
	if !ok {
		t.Fatal("want the pre-restart escalate recovered, got not-found")
	}
	if diff := cmp.Diff(escalated, got); diff != "" {
		t.Error("wrong Governed recovered (-want +got)", diff)
	}
}

func TestRebuildHolds_ExcludesAFingerprintAlreadyResolved(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}
	pub := publish.NewJetPublisher[decision.Governed](js)

	// the sequence a real approveHandler produces: hold, then re-issue
	if err := pub.Publish(ctx, "thump.decisions", decision.Governed{Decision: decision.Decision{SignalRef: "fp-1", Verdict: decision.VerdictHold}}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, "thump.decisions", decision.Governed{Decision: decision.Decision{SignalRef: "fp-1", Verdict: decision.VerdictApproved}}); err != nil {
		t.Fatal(err)
	}

	holds, err := hiss.RebuildHoldsForTest(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := holds.Take("fp-1"); ok {
		t.Error("want an already-approved fingerprint absent from rebuilt holds, got present")
	}
}
