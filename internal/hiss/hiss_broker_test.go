package hiss_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/wire"
)

// TestHissBroker_ConsumesProposalsAndPublishesDecisions is hiss's slice of
// the five-beat seam: a ProposalSet published to thump.proposals must come
// out the other side as a Governed decision on thump.decisions, over the
// embedded (keyless) broker — the same handle a dir Tick calls, fed by a
// JetSubscriber instead of a glob.
func TestHissBroker_ConsumesProposalsAndPublishesDecisions(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	walDir := t.TempDir()
	w := &publish.WAL{Dir: walDir, Beat: "hiss", Subject: "thump.decisions"}
	defer func() { _ = w.Close(ctx) }()

	tr := &hiss.Transport{
		Pub:    &publish.WALPublisher[decision.Governed]{WAL: w, Next: publish.NewJetPublisher[decision.Governed](js)},
		Policy: calmPolicy(),
		Log:    hiss.NewDecisionLog(),
		Now:    frozenNow,
	}

	sub := broker.NewJetSubscriber[proposal.Set](js)
	subCtx, stopSub := context.WithCancel(ctx)
	defer stopSub()
	done := make(chan error, 1)
	go func() { done <- sub.Run(subCtx, "thump.proposals", tr.HandleForTest) }()

	pub := publish.NewJetPublisher[proposal.Set](js)
	if err := pub.Publish(ctx, "thump.proposals", governedSet()); err != nil {
		t.Fatal("publish:", err)
	}

	stream, err := js.Stream(ctx, "THUMP")
	if err != nil {
		t.Fatal(err)
	}

	var got decision.Governed
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, mErr := stream.GetLastMsgForSubject(ctx, "thump.decisions")
		if mErr == nil {
			if uErr := wire.Unmarshal(raw.Data, &got); uErr != nil {
				t.Fatal("wire bytes didn't decode:", uErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("decision never landed on thump.decisions:", mErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stopSub()
	<-done

	if diff := cmp.Diff(goldenDecision(), got.Decision); diff != "" {
		t.Error("decision didn't survive the broker round trip (-want +got)", diff)
	}
}
