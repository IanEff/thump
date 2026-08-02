package broker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
)

func TestDrainSubject_FoldsEveryPublishedMessageInStreamOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "two"}); err != nil {
		t.Fatal(err)
	}

	var got []string
	err := broker.DrainSubject(ctx, js, "thump.detections", "test drain", func(_ time.Time, d signal.Detection) {
		got = append(got, d.Fingerprint)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("want [one two] folded in publish order, got %v", got)
	}
}

func TestDrainSubject_SkipsAnUndecodableMessage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	js := natstest.New(t)
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	if _, err := js.Publish(ctx, "thump.detections", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "good"}); err != nil {
		t.Fatal(err)
	}

	var got []string
	err := broker.DrainSubject(ctx, js, "thump.detections", "test drain", func(_ time.Time, d signal.Detection) {
		got = append(got, d.Fingerprint)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "good" {
		t.Fatalf("want the poison message skipped and the good one folded, got %v", got)
	}
}

func TestDrainSubject_CreateConsumerFailureIsWrappedWithPrefix(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithCancel(t.Context())
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}
	cancel() // guarantees CreateConsumer fails, deterministically, without depending on server internals

	err := broker.DrainSubject(ctx, js, "thump.detections", "widget: rebuild", func(_ time.Time, _ signal.Detection) {})
	if err == nil {
		t.Fatal("want an error from a cancelled context, got nil")
	}
	if !strings.HasPrefix(err.Error(), "widget: rebuild: create consumer:") {
		t.Errorf("want the caller-supplied prefix to lead the error, got %q", err.Error())
	}
}
