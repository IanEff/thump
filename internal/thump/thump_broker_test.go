package thump_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/thump"
	"github.com/ianeff/thump/internal/wire"
)

// TestThumpBroker_ConsumesDecisionsAndPublishesOrdersAndOutcomes is thump's
// slice of the five-beat seam: a Governed approval published to
// thump.decisions must come out the other side as both an Order on
// thump.orders and an Outcome on thump.outcomes, over the embedded
// (keyless) broker — the same handle a dir Tick calls, fed by a
// JetSubscriber instead of a glob.
func TestThumpBroker_ConsumesDecisionsAndPublishesOrdersAndOutcomes(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	walDir := t.TempDir()
	wOrders := &publish.WAL{Dir: walDir, Beat: "thump", Subject: "thump.orders"}
	defer func() { _ = wOrders.Close(ctx) }()
	wOutcomes := &publish.WAL{Dir: walDir, Beat: "thump", Subject: "thump.outcomes"}
	defer func() { _ = wOutcomes.Close(ctx) }()

	tr := &thump.Transport{
		OrderPub:   &publish.WALPublisher[thump.Order]{WAL: wOrders, Next: publish.NewJetPublisher[thump.Order](js)},
		OutcomePub: &publish.WALPublisher[outcome.Outcome]{WAL: wOutcomes, Next: publish.NewJetPublisher[outcome.Outcome](js)},
		Catalog:    richCatalog(),
		Log:        thump.NewOutcomeLog(),
		Exec:       thump.DryRun{},
		Now:        frozenNow,
	}

	sub := broker.NewJetSubscriber[decision.Governed](js)
	subCtx, stopSub := context.WithCancel(ctx)
	defer stopSub()
	done := make(chan error, 1)
	go func() { done <- sub.Run(subCtx, "thump.decisions", tr.HandleForTest) }()

	pub := publish.NewJetPublisher[decision.Governed](js)
	if err := pub.Publish(ctx, "thump.decisions", approvedGoverned()); err != nil {
		t.Fatal("publish:", err)
	}

	stream, err := js.Stream(ctx, "THUMP")
	if err != nil {
		t.Fatal(err)
	}

	var gotOrder thump.Order
	var gotOutcome outcome.Outcome
	deadline := time.Now().Add(5 * time.Second)
	for {
		orderMsg, oErr := stream.GetLastMsgForSubject(ctx, "thump.orders")
		outcomeMsg, ocErr := stream.GetLastMsgForSubject(ctx, "thump.outcomes")
		if oErr == nil && ocErr == nil {
			if err := wire.Unmarshal(orderMsg.Data, &gotOrder); err != nil {
				t.Fatal("order wire bytes didn't decode:", err)
			}
			if err := wire.Unmarshal(outcomeMsg.Data, &gotOutcome); err != nil {
				t.Fatal("outcome wire bytes didn't decode:", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("order and/or outcome never landed on the stream:", oErr, ocErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stopSub()
	<-done

	if diff := cmp.Diff(goldenOrder(), gotOrder); diff != "" {
		t.Error("order didn't survive the broker round trip (-want +got)", diff)
	}
	if diff := cmp.Diff(goldenOutcome(), gotOutcome); diff != "" {
		t.Error("outcome didn't survive the broker round trip (-want +got)", diff)
	}
}
