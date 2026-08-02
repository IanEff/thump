// Package poll is the offline-transport ticker every beat's dir-poll
// (rather than broker-mode) path drives its Tick through — a plain
// fixed-interval loop, or a jittered exponential backoff, both bounded per
// tick by an authored timeout rather than left to inherit the beat's own
// long-lived context.
package poll

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

// Config selects the offline dir-poll cadence. A zero Backoff is the plain
// fixed-interval ticker hiss and thump use; a non-nil Backoff opts into clank's
// jittered exponential backoff, which slows a beat down while its inbox source
// is failing and snaps back to Base once a tick succeeds.
type Config struct {
	Interval time.Duration
	Backoff  *BackoffConfig
	// Timeout bounds one tick. A zero value leaves the tick unbounded — every
	// call site must choose a number rather than silently inheriting one.
	Timeout time.Duration
}

// DefaultConfig is the fixed-interval cadence hiss and thump's offline
// dir-poll transports share: tick every 5s, bounded to 20s.
var DefaultConfig = Config{Interval: 5 * time.Second, Timeout: 20 * time.Second}

// BackoffConfig is the growth schedule for a failing poll loop: start at Base,
// double on each failed tick up to Cap, reset to Base on success. When a tick
// fails, up to Base/JitterDivisor of jitter is added so many beats don't
// resynchronize their retries into a thundering herd.
type BackoffConfig struct {
	Base, Cap     time.Duration
	JitterDivisor int
}

// Loop drives tick on the configured cadence until ctx is cancelled,
// logging (never returning) a tick error. It is the offline transport: broker
// mode uses RunConsumer instead. Single-threaded by construction — tick N+1
// never starts until tick N returns, so cfg.Timeout is what keeps one slow
// tick from stacking up behind the next.
func Loop(ctx context.Context, cfg Config, tick func(context.Context) error) {
	tick = WithTimeout(cfg.Timeout, tick)
	if cfg.Backoff == nil {
		pollFixed(ctx, cfg.Interval, tick)
		return
	}
	pollBackoff(ctx, *cfg.Backoff, tick)
}

// WithTimeout wraps tick so each call gets its own deadline. A beat's own
// context only cancels on SIGTERM, so without this a tick that makes N
// sequential backend calls can run N times the per-call bound and overlap the
// next tick. A zero d returns tick unchanged.
func WithTimeout(d time.Duration, tick func(context.Context) error) func(context.Context) error {
	if d == 0 {
		return tick
	}
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return tick(ctx)
	}
}

func pollFixed(ctx context.Context, interval time.Duration, tick func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return
		case <-ticker.C:
			if err := tick(ctx); err != nil {
				slog.Error("tick failed", "err", err)
			}
		}
	}
}

func pollBackoff(ctx context.Context, cfg BackoffConfig, tick func(context.Context) error) {
	delay := cfg.Base
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("shutting down")
			return
		case <-timer.C:
			err := tick(ctx)
			if err != nil {
				slog.Error("tick failed", "err", err)
			}
			delay = nextDelay(cfg, delay, err == nil)
			if err != nil && cfg.JitterDivisor > 0 {
				delay += rand.N(delay / time.Duration(cfg.JitterDivisor)) //nolint:gosec // G404: retry jitter, not a security-sensitive value
			}
			timer.Reset(delay)
		}
	}
}

// nextDelay grows the backoff toward Cap on failure and resets it to Base on
// success. Jitter is applied by the caller (a random value can't be pinned to a
// table test's want), so this stays a pure function.
func nextDelay(cfg BackoffConfig, cur time.Duration, tickOK bool) time.Duration {
	if tickOK {
		return cfg.Base
	}
	return min(cur*2, cfg.Cap)
}
