package beat

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"
)

// PollConfig selects the offline dir-poll cadence. A zero Backoff is the plain
// fixed-interval ticker hiss and thump use; a non-nil Backoff opts into clank's
// jittered exponential backoff, which slows a beat down while its inbox source
// is failing and snaps back to Base once a tick succeeds.
type PollConfig struct {
	Interval time.Duration
	Backoff  *BackoffConfig
}

// BackoffConfig is the growth schedule for a failing poll loop: start at Base,
// double on each failed tick up to Cap, reset to Base on success. When a tick
// fails, up to Base/JitterDivisor of jitter is added so many beats don't
// resynchronize their retries into a thundering herd.
type BackoffConfig struct {
	Base, Cap     time.Duration
	JitterDivisor int
}

// PollLoop drives tick on the configured cadence until ctx is cancelled,
// logging (never returning) a tick error. It is the offline transport: broker
// mode uses RunConsumer instead.
func PollLoop(ctx context.Context, cfg PollConfig, tick func(context.Context) error) {
	if cfg.Backoff == nil {
		pollFixed(ctx, cfg.Interval, tick)
		return
	}
	pollBackoff(ctx, *cfg.Backoff, tick)
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
				delay += rand.N(delay / time.Duration(cfg.JitterDivisor)) //nolint:gosec
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
