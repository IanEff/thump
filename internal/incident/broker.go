package incident

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/nats-io/nats.go/jetstream"
)

// SnapshotBroker builds a Projection from the shared stream's full
// history -- the broker-mode counterpart to a disk Snapshot for the
// four subjects that carry a fingerprint.
func SnapshotBroker(ctx context.Context, js jetstream.JetStream) (*Projection, error) {
	var events []brokerEvent

	if err := broker.DrainSubject(ctx, js, "thump.detections", "incident: snapshot broker", func(at time.Time, v signal.Detection) {
		events = append(events, brokerEvent{at: at, obj: v})
	}); err != nil {
		return nil, fmt.Errorf("incident: snapshot broker: %w", err)
	}
	if err := broker.DrainSubject(ctx, js, "thump.proposals", "incident: snapshot broker", func(at time.Time, v proposal.Set) {
		events = append(events, brokerEvent{at: at, obj: v})
	}); err != nil {
		return nil, fmt.Errorf("incident: snapshot broker: %w", err)
	}
	if err := broker.DrainSubject(ctx, js, "thump.decisions", "incident: snapshot broker", func(at time.Time, v decision.Governed) {
		events = append(events, brokerEvent{at: at, obj: v})
	}); err != nil {
		return nil, fmt.Errorf("incident: snapshot broker: %w", err)
	}
	if err := broker.DrainSubject(ctx, js, "thump.outcomes", "incident: snapshot broker", func(at time.Time, v outcome.Outcome) {
		events = append(events, brokerEvent{at: at, obj: v})
	}); err != nil {
		return nil, fmt.Errorf("incident: snapshot broker: %w", err)
	}

	sort.Slice(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })

	proj := NewProjection()
	for _, ev := range events {
		if err := proj.Apply(ev.obj); err != nil {
			return nil, fmt.Errorf("incident: snapshot broker: %w", err)
		}
	}
	return proj, nil
}

// brokerEvent is just one historical message from any of the four finger-
// printed subjects, tagged with the timestamp JetStream stored at it.
type brokerEvent struct {
	at  time.Time
	obj any
}
