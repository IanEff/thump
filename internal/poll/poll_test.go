package poll_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/internal/poll"
)

// TestWithTimeout_CutsATickThatOverrunsItsDeadline pins the bound one level
// above the HTTP client: a tick making N sequential backend calls is bounded
// by N times the per-call timeout, which is not a bound at all once N is the
// length of the watch list.
func TestWithTimeout_CutsATickThatOverrunsItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tick := poll.WithTimeout(45*time.Second, func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		start := time.Now()

		err := tick(t.Context())

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("an overrunning tick must end in its own deadline, got %v", err)
		}
		if got := time.Since(start); got != 45*time.Second {
			t.Errorf("tick ran %v, want the configured 45s bound", got)
		}
	})
}

// TestWithTimeout_ZeroLeavesTheTickUnbounded pins the opt-out: a call site
// that hasn't chosen a number yet keeps the behaviour it had, rather than
// silently inheriting one.
func TestWithTimeout_ZeroLeavesTheTickUnbounded(t *testing.T) {
	t.Parallel()
	var gotDeadline bool
	err := poll.WithTimeout(0, func(ctx context.Context) error {
		_, gotDeadline = ctx.Deadline()
		return nil
	})(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if gotDeadline {
		t.Error("a zero timeout must not put a deadline on the tick's context")
	}
}

func TestLoop_ReturnsPromptlyOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the loop must return without ever ticking

	ticked := false
	done := make(chan struct{})
	go func() {
		poll.Loop(ctx, poll.Config{Interval: time.Hour}, func(context.Context) error {
			ticked = true
			return nil
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Loop did not return on a cancelled context")
	}
	if ticked {
		t.Error("Loop ticked despite the context being cancelled")
	}
}
