package clank_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
)

// TestRedelivery_IsAFingerprintNoOp proves the JetStream path reuses the
// ledger's existing dedup (I-14: keyed on SignalRef) instead of inventing a
// NATS-sequence-based shortcut. The same Detection crosses the broker twice;
// only one open ProposalSet should exist afterward.
func TestRedelivery_IsAFingerprintNoOp(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	det := signal.Detection{Fingerprint: "slo_burn:ceph-rgw", ServiceTier: "tier-1"}
	pub := publish.NewJetPublisher[signal.Detection](js)
	if err := pub.Publish(ctx, "thump.detections", det); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, "thump.detections", det); err != nil { // the "redelivery"
		t.Fatal(err)
	}

	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}
	eng, _ := newTestEngine(model)

	sub := broker.NewJetSubscriber[signal.Detection](js)
	done := make(chan error, 1)
	go func() {
		done <- sub.Run(ctx, "thump.detections", func(ctx context.Context, d signal.Detection) error {
			_, err := eng.Propose(ctx, d)
			return err
		})
	}()

	// give both deliveries a moment to land, then check the ledger.
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done

	open, err := eng.Ledger.Open(context.Background(), det.Fingerprint, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("want exactly one open ProposalSet for the fingerprint, got %d", len(open))
	}
}
