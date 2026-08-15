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
