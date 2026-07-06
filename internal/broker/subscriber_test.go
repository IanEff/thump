package broker_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
)

func TestSubscriber_DeliversAndHandsToHandler(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "slo_burn:ceph-rgw"}); err != nil {
		t.Fatal(err)
	}

	got := make(chan signal.Detection, 1)
	runErr := make(chan error, 1)
	sub := broker.NewJetSubscriber[signal.Detection](js)

	go func() {
		runErr <- sub.Run(ctx, "thump.detections", func(_ context.Context, d signal.Detection) error {
			got <- d
			return nil
		})
	}()

	select {
	case d := <-got:
		if diff := cmp.Diff("slo_burn:ceph-rgw", d.Fingerprint); diff != "" {
			t.Error("fingerprint didn't survive the broker (-want +got)", diff)
		}
	case err := <-runErr:
		t.Fatal("subscriber run exited early:", err)
	case <-ctx.Done():
		t.Fatal("handler never received the message")
	}
}
