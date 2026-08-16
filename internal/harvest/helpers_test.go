package harvest_test

import (
	"context"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// feedWatcher replays a fixed sequence of Results on one fingerprint,
// then closes -- a scripted convergence watcher for tests that don't
// need a real one.
type feedWatcher []outcome.Result

func (f feedWatcher) Outcomes(context.Context) (<-chan outcome.Outcome, error) {
	ch := make(chan outcome.Outcome, len(f))
	for _, r := range f {
		ch <- outcome.Outcome{SignalRef: "slo_burn:ceph-cluster", Result: r}
	}
	close(ch)
	return ch, nil
}

type silentWatcher struct{}

func (silentWatcher) Outcomes(context.Context) (<-chan outcome.Outcome, error) {
	return make(chan outcome.Outcome), nil
}

// feedSetWatcher replays a fixed sequence of Sets, then closes — a
// scripted SetWatcher for tests exercising the confidence-enrichment path.
// A nil/empty feedSetWatcher is the harmless case: it closes immediately,
// and firstSetFor reports ok=false rather than blocking.
type feedSetWatcher []proposal.Set

func (f feedSetWatcher) Sets(context.Context) (<-chan proposal.Set, error) {
	ch := make(chan proposal.Set, len(f))
	for _, s := range f {
		ch <- s
	}
	close(ch)
	return ch, nil
}

// feedDeclineWatcher, feedHeldWatcher, and feedDetectionWatcher mirror
// feedWatcher for Settle's other three legs — a fixed sequence, then closed.
type feedDeclineWatcher []decision.Decision

func (f feedDeclineWatcher) Declines(context.Context) (<-chan decision.Decision, error) {
	ch := make(chan decision.Decision, len(f))
	for _, d := range f {
		ch <- d
	}
	close(ch)
	return ch, nil
}

type feedHeldWatcher []decision.Governed

func (f feedHeldWatcher) Held(context.Context) (<-chan decision.Governed, error) {
	ch := make(chan decision.Governed, len(f))
	for _, g := range f {
		ch <- g
	}
	close(ch)
	return ch, nil
}

type feedDetectionWatcher []signal.Detection

func (f feedDetectionWatcher) Detections(context.Context) (<-chan signal.Detection, error) {
	ch := make(chan signal.Detection, len(f))
	for _, d := range f {
		ch <- d
	}
	close(ch)
	return ch, nil
}

// orderedSetThenTerminal hands Settle a Set over an unbuffered channel, then
// only after that send has been received does it make outcomes/declines
// available — pins real-world ordering (a Set always precedes the
// outcome/decline it caused) without relying on select's pseudo-random
// tie-break among simultaneously-ready channels, which a pre-buffered fixture
// like feedWatcher/feedSetWatcher can't guarantee when fed together.
type orderedSetThenTerminal struct {
	set      proposal.Set
	outcomes []outcome.Outcome
	declines []decision.Decision
	setSent  chan struct{}
}

func newOrderedSetThenTerminal(set proposal.Set) *orderedSetThenTerminal {
	return &orderedSetThenTerminal{set: set, setSent: make(chan struct{})}
}

func (o *orderedSetThenTerminal) Sets(ctx context.Context) (<-chan proposal.Set, error) {
	ch := make(chan proposal.Set)
	go func() {
		select {
		case ch <- o.set:
			close(o.setSent)
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func (o *orderedSetThenTerminal) Outcomes(ctx context.Context) (<-chan outcome.Outcome, error) {
	ch := make(chan outcome.Outcome)
	go func() {
		<-o.setSent
		for _, oc := range o.outcomes {
			select {
			case ch <- oc:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (o *orderedSetThenTerminal) Declines(ctx context.Context) (<-chan decision.Decision, error) {
	ch := make(chan decision.Decision)
	go func() {
		<-o.setSent
		for _, d := range o.declines {
			select {
			case ch <- d:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// orderedSetsThenOutcome sends every set in order over an unbuffered
// channel, then only once all of them have been received does it make the
// outcome available — like orderedSetThenTerminal, but for the replayed-set
// case where more than one Set for the same signalRef needs to land before
// Settle's terminal leg fires.
type orderedSetsThenOutcome struct {
	sets     []proposal.Set
	outcomes []outcome.Outcome
	allSent  chan struct{}
}

func newOrderedSetsThenOutcome(sets []proposal.Set, outcomes []outcome.Outcome) *orderedSetsThenOutcome {
	return &orderedSetsThenOutcome{sets: sets, outcomes: outcomes, allSent: make(chan struct{})}
}

func (o *orderedSetsThenOutcome) Sets(ctx context.Context) (<-chan proposal.Set, error) {
	ch := make(chan proposal.Set)
	go func() {
		defer close(o.allSent)
		for _, s := range o.sets {
			select {
			case ch <- s:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (o *orderedSetsThenOutcome) Outcomes(ctx context.Context) (<-chan outcome.Outcome, error) {
	ch := make(chan outcome.Outcome)
	go func() {
		<-o.allSent
		for _, oc := range o.outcomes {
			select {
			case ch <- oc:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
