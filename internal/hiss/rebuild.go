package hiss

import (
	"context"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/broker"
	"github.com/nats-io/nats.go/jetstream"
)

// rebuildHolds replays hiss's own thump.decisions history and returns a
// PendingHolds seeded with every fingerprint whose latest decision was
// still VerdictHold, rebuilding what a restart would otherwise drop —
// bounded by the shared stream's 48h retention, not a permanent archive.
func rebuildHolds(ctx context.Context, js jetstream.JetStream) (*PendingHolds, error) {
	latest := make(map[string]decision.Governed)
	if err := broker.DrainSubject(ctx, js, "thump.decisions", "hiss: rebuild holds", func(_ time.Time, g decision.Governed) {
		latest[g.Decision.SignalRef] = g
	}); err != nil {
		return nil, err
	}

	holds := NewPendingHolds()
	for _, g := range latest {
		if g.Decision.Verdict.AwaitsApprival() {
			holds.Record(g)
		}
	}

	return holds, nil
}
