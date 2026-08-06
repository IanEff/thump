package harvest_test

import (
	"context"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
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
