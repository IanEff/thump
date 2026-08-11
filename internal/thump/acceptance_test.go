package thump_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/thump"
)

// fakeReleaseProbe is a minimal canned-answer fake of thump.ReleaseProbe,
// mirroring recordForge.Withdraw (internal/actuate/runner_test.go) — not
// behavioral, just programmed with what to answer.
type fakeReleaseProbe struct {
	accepted bool
	err      error
	calls    []string // every key Withdraw was asked about, in order
}

func (f *fakeReleaseProbe) Withdraw(_ context.Context, key string) (bool, error) {
	f.calls = append(f.calls, key)
	return f.accepted, f.err
}

func TestPoll_WithdrawsTheForwardKeyAfterTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		probe := &fakeReleaseProbe{accepted: true}
		w := thump.AcceptanceWatcher{Probe: probe}

		got, err := w.Poll(context.Background(), goldenOrder())

		if err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Error("wrong acceptance verdict, want the probe's accepted answer")
		}
		if diff := cmp.Diff([]string{goldenOrder().ContractRef}, probe.calls); diff != "" {
			t.Error("wrong withdraw key (-want +got)", diff)
		}
	})
}

func TestPoll_ReturnsCtxErrOnACancelledWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		probe := &fakeReleaseProbe{accepted: true}
		w := thump.AcceptanceWatcher{Probe: probe}
		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			time.Sleep(1 * time.Second) // well inside the window, before it elapses
			cancel()
		}()

		got, err := w.Poll(ctx, goldenOrder())

		if !errors.Is(err, context.Canceled) {
			t.Errorf("wrong error for a cancelled wait, got %v", err)
		}
		if got {
			t.Error("a cancelled wait must not report acceptance")
		}
		if len(probe.calls) != 0 {
			t.Errorf("a cancelled wait must never reach the probe, got %d calls", len(probe.calls))
		}
	})
}
