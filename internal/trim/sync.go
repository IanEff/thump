package trim

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/wire"
)

// NATSSync materializes the four boundary-object subjects Fold understands
// (detections, proposals, decisions, outcomes — never declines or
// approvals, which Fold's type switch doesn't model) off the shared
// JetStream stream into Inbox's filesystem layout — the bridge Transport
// needs on a rig where nothing else ever writes that layout to disk. NATS
// is the only inter-beat transport in production; this exists so
// Transport, Projection, and Fold never have to know that.
type NATSSync struct {
	JS    jetstream.JetStream
	Inbox string
}

// Run drains detections, then proposals, then decisions, then outcomes —
// same fixed order as Transport.Tick, for the same reason: Fold applies an
// object unconditionally by type, never by comparing timestamps, so a
// fingerprint's Incident only lands on the right Stage if every earlier
// pipeline stage's objects are written (and therefore later read) before
// any later stage's. Returns the total number of objects written.
func (s *NATSSync) Run(ctx context.Context) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}

	total := 0
	n, err := syncSubject[signal.Detection](ctx, s.JS, "thump.detections", filepath.Join(s.Inbox, "detections"))
	total += n
	if err != nil {
		return total, err
	}
	n, err = syncSubject[proposal.Set](ctx, s.JS, "thump.proposals", filepath.Join(s.Inbox, "proposals"))
	total += n
	if err != nil {
		return total, err
	}
	n, err = syncSubject[decision.Governed](ctx, s.JS, "thump.decisions", filepath.Join(s.Inbox, "decisions"))
	total += n
	if err != nil {
		return total, err
	}
	n, err = syncSubject[outcome.Outcome](ctx, s.JS, "thump.outcomes", filepath.Join(s.Inbox, "outcomes"))
	total += n
	if err != nil {
		return total, err
	}
	return total, nil
}

// syncSubject drains every message currently retained on subject through a
// plain ephemeral pull consumer (no Durable name — never touches
// broker.DurableFor's fixed names, so it can never compete with or reset a
// live beat's own read position) with AckNonePolicy, since sync only reads.
// It deliberately does NOT use jetstream's OrderedConsumer: an ordered
// consumer resets itself on every single FetchNoWait call (nats.go's own
// doc comment on OrderedConsumer.FetchNoWait: "it will reset the consumer
// for each subsequent Fetch call... consider Consume or Messages instead"),
// which starved this drain loop — a plain ephemeral consumer has no such
// per-call reset. FetchNoWait returns exactly what's on the subject right
// now rather than waiting for more to arrive, matching Transport.Snapshot's
// own one-shot, no-memory-between-calls contract. Each object is written
// under dir named by its stream sequence number, zero-padded so lexical
// filename order equals emission order — required because a fingerprint
// can appear twice on thump.decisions (held, then later approved via trim
// approve) and Fold has no other way to tell which one is newer.
func syncSubject[T any](ctx context.Context, js jetstream.JetStream, subject, dir string) (int, error) {
	cons, err := js.CreateConsumer(ctx, broker.StreamName, jetstream.ConsumerConfig{
		FilterSubject:     subject,
		DeliverPolicy:     jetstream.DeliverAllPolicy,
		AckPolicy:         jetstream.AckNonePolicy,
		InactiveThreshold: 30 * time.Second, // self-cleans even if the explicit delete below never runs
	})
	if err != nil {
		return 0, fmt.Errorf("trim: create consumer %s: %w", subject, err)
	}
	defer func() { _ = js.DeleteConsumer(ctx, broker.StreamName, cons.CachedInfo().Name) }()

	n := 0
	for {
		batch, err := cons.FetchNoWait(100)
		if err != nil {
			return n, fmt.Errorf("trim: fetch %s: %w", subject, err)
		}

		got := 0
		for msg := range batch.Messages() {
			got++
			md, err := msg.Metadata()
			if err != nil {
				continue // no sequence number to name the file with — skip, don't fail the pass
			}

			var v T
			if err := wire.Unmarshal(msg.Data(), &v); err != nil {
				continue // read-only pass — mirrors Transport.snapshotDir's own skip-what-Tick-would-quarantine behavior
			}

			seq := md.Sequence.Stream
			pub := &publish.DirPublisher[T]{
				Dir:  dir,
				Name: func(T) string { return fmt.Sprintf("%020d", seq) },
			}
			if err := pub.Publish(ctx, subject, v); err != nil {
				return n, fmt.Errorf("trim: write %s: %w", subject, err)
			}
			n++
		}
		if err := batch.Error(); err != nil {
			return n, fmt.Errorf("trim: batch %s: %w", subject, err)
		}
		if got == 0 {
			return n, nil
		}
	}
}
