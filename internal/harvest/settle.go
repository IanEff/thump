package harvest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
)

// ErrSettleTimeout means the settle window elapsed with no terminal
// outcome on the fingerprint, and no detection ever arrived to make a
// clank refusal ("refused") distinguishable from a rig that never fired.
var ErrSettleTimeout = errors.New("harvest: settle window elapsed with no terminal outcome")

// Watcher is how a harvest learns an incident was rendered or executed.
// Production satisfies it by consuming thump.outcomes; test satisfies it
// with a channel.
type Watcher interface {
	Outcomes(ctx context.Context) (<-chan outcome.Outcome, error)
}

// DeclineWatcher is how a harvest learns hiss declined a Set outright —
// escalate or rejected. Production consumes thump.declines; test satisfies
// it with a channel.
type DeclineWatcher interface {
	Declines(ctx context.Context) (<-chan decision.Decision, error)
}

// HeldWatcher is how a harvest learns hiss held a Set for a human ack.
// Production consumes thump.held; test satisfies it with a channel.
type HeldWatcher interface {
	Held(ctx context.Context) (<-chan decision.Governed, error)
}

// DetectionWatcher is how a harvest learns rattle detected the row's
// fingerprint at all. It is the only leg that fires on a clank refusal: a
// refused Set is journaled WAL-only, with no broker subject, by design (I-10
// — hiss must never structurally see an ungated Set), so nothing else in
// Legs ever reports it. Production consumes thump.detections; test satisfies
// it with a channel.
type DetectionWatcher interface {
	Detections(ctx context.Context) (<-chan signal.Detection, error)
}

// Legs bundles every broker leg Settle selects across. A nil field means
// that leg never fires and Settle skips it — a test exercising only the
// outcome leg doesn't have to fake subjects it doesn't care about.
type Legs struct {
	Outcomes   Watcher
	Declines   DeclineWatcher
	Held       HeldWatcher
	Detections DetectionWatcher
	Sets       SetWatcher // thump.proposals — the refusal leg's cancel signal, not a terminal on its own
}

// Terminal is how a scenario ended, whatever the ending was. Refusing is as
// terminal as settling — for a fault no catalogued action can clear,
// refusing is the correct answer — and the beat that refuses is part of the
// answer: a fault clank declines to propose on and one hiss holds are
// different engine behaviours, and a harness that collapses them hides a
// regression in either.
type Terminal struct {
	Verdict          string         // approved, held, declined, refused
	Result           outcome.Result // zero for anything but approved — nothing executed
	ContractRef      string
	DecisionRef      string
	ObservedSeverity *float64 // set only when Verdict is approved — the convergence watcher's measured end state
}

// isTerminal names the settled results explicitly rather than excluding
// ResultApplied — a zero-valued Result is not a settled one, and an
// exclusion test reads it as terminal and ends the wait on a record that
// says nothing.
func isTerminal(r outcome.Result) bool {
	switch r {
	case outcome.ResultSuccess,
		outcome.ResultFailure,
		outcome.ResultPartialNonConverging,
		outcome.ResultBlocked,
		outcome.ResultUnknown,
		outcome.ResultRendered:
		return true
	default:
		return false
	}
}

// Settle blocks until legs reports a terminal state for signalRef — an
// outcome, a decline, a hold, or (once a detection arrives with no proposal
// inside refusalGrace) a clank refusal — or window elapses.
func Settle(ctx context.Context, legs Legs, signalRef string, window, refusalGrace time.Duration) (Terminal, error) {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	outcomes, err := openOutcomes(ctx, legs.Outcomes)
	if err != nil {
		return Terminal{}, err
	}
	declines, err := openDeclines(ctx, legs.Declines)
	if err != nil {
		return Terminal{}, err
	}
	held, err := openHeld(ctx, legs.Held)
	if err != nil {
		return Terminal{}, err
	}
	detections, err := openDetections(ctx, legs.Detections)
	if err != nil {
		return Terminal{}, err
	}
	sets, err := openSets(ctx, legs.Sets)
	if err != nil {
		return Terminal{}, err
	}

	var refusalTimer *time.Timer
	var refusalC <-chan time.Time
	detected := false

	for {
		select {
		case <-ctx.Done():
			return Terminal{}, fmt.Errorf("%w: %s", ErrSettleTimeout, signalRef)
		case <-refusalC:
			return Terminal{Verdict: "refused"}, nil
		case o, ok := <-outcomes:
			if !ok {
				outcomes = nil
				continue
			}
			if o.SignalRef != signalRef || !isTerminal(o.Result) {
				continue
			}
			return Terminal{
				Verdict:          "approved",
				Result:           o.Result,
				ContractRef:      o.ContractRef,
				DecisionRef:      o.DecisionRef,
				ObservedSeverity: o.ObservedSeverity,
			}, nil
		case d, ok := <-declines:
			if !ok {
				declines = nil
				continue
			}
			if d.SignalRef != signalRef {
				continue
			}
			return Terminal{Verdict: "declined", DecisionRef: d.ID}, nil
		case g, ok := <-held:
			if !ok {
				held = nil
				continue
			}
			if g.Decision.SignalRef != signalRef {
				continue
			}
			return Terminal{
				Verdict:     "held",
				ContractRef: g.Set.ContractRefFor(g.Decision.CandidateRef),
				DecisionRef: g.Decision.ID,
			}, nil
		case det, ok := <-detections:
			if !ok {
				detections = nil
				continue
			}
			if det.Fingerprint != signalRef || detected {
				continue
			}
			detected = true
			refusalTimer = time.NewTimer(refusalGrace)
			refusalC = refusalTimer.C
		case s, ok := <-sets:
			if !ok {
				sets = nil
				continue
			}
			if s.SignalRef != signalRef {
				continue
			}
			// A Set arrived for this fingerprint — clank did not refuse it.
			// Cancel any pending refusal timer and keep waiting for whichever
			// terminal leg fires next.
			if refusalTimer != nil {
				refusalTimer.Stop()
			}
			refusalC = nil
		}
	}
}

func openOutcomes(ctx context.Context, w Watcher) (<-chan outcome.Outcome, error) {
	if w == nil {
		return nil, nil
	}
	ch, err := w.Outcomes(ctx)
	if err != nil {
		return nil, fmt.Errorf("harvest: watch outcomes: %w", err)
	}
	return ch, nil
}

func openDeclines(ctx context.Context, w DeclineWatcher) (<-chan decision.Decision, error) {
	if w == nil {
		return nil, nil
	}
	ch, err := w.Declines(ctx)
	if err != nil {
		return nil, fmt.Errorf("harvest: watch declines: %w", err)
	}
	return ch, nil
}

func openHeld(ctx context.Context, w HeldWatcher) (<-chan decision.Governed, error) {
	if w == nil {
		return nil, nil
	}
	ch, err := w.Held(ctx)
	if err != nil {
		return nil, fmt.Errorf("harvest: watch held: %w", err)
	}
	return ch, nil
}

func openDetections(ctx context.Context, w DetectionWatcher) (<-chan signal.Detection, error) {
	if w == nil {
		return nil, nil
	}
	ch, err := w.Detections(ctx)
	if err != nil {
		return nil, fmt.Errorf("harvest: watch detections: %w", err)
	}
	return ch, nil
}

func openSets(ctx context.Context, w SetWatcher) (<-chan proposal.Set, error) {
	if w == nil {
		return nil, nil
	}
	ch, err := w.Sets(ctx)
	if err != nil {
		return nil, fmt.Errorf("harvest: watch sets: %w", err)
	}
	return ch, nil
}
