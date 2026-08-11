package thump_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

func TestWatchAndAccept_RecordsPartialNonConvergingWithTheAuthoredFallbackWhenNobodyMerges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inbox, outbox := t.TempDir(), t.TempDir()
		writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())

		runner := &fakeRunner{result: outcome.ResultProposed}
		tr := newTestTransport(inbox, outbox)
		tr.Exec = thump.Live{Runner: runner}
		tr.Acceptance = &thump.AcceptanceWatcher{Probe: &fakeReleaseProbe{accepted: false}}

		if err := tr.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()                          // poll goroutine reaches its timer block
		time.Sleep(goldenOrder().Success.Window) // fake clock jumps the acceptance window
		synctest.Wait()                          // poll goroutine finishes its post-window work

		partial := tr.Log.ByResult(outcome.ResultPartialNonConverging)
		if len(partial) != 1 {
			t.Fatalf("want exactly one partial_non_converging outcome, got %d", len(partial))
		}
		if got := partial[0].Error; got == "" {
			t.Error("a not-accepted release must carry the authored fallback, got no error text")
		}
		if n := len(tr.Log.ByResult(outcome.ResultSuccess)); n != 0 {
			t.Errorf("a release nobody merged must never reach a convergence watch, got %d success outcomes", n)
		}
	})
}

func TestWatchAndAccept_FallsIntoTheExistingConvergenceWatchOnceAccepted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inbox, outbox := t.TempDir(), t.TempDir()
		writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())

		runner := &fakeRunner{result: outcome.ResultProposed}
		tr := newTestTransport(inbox, outbox)
		tr.Exec = thump.Live{Runner: runner}
		tr.Acceptance = &thump.AcceptanceWatcher{Probe: &fakeReleaseProbe{accepted: true}}
		tr.Reversal = &thump.ReversalWatcher{
			Probe: thump.PrometheusConverger{Probe: &fakeProbe{answer: true, severity: 0.1, severityOK: true}},
			Now:   frozenNow,
		}

		if err := tr.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()                          // poll goroutine reaches its acceptance timer
		time.Sleep(goldenOrder().Success.Window) // fake clock jumps the acceptance window
		synctest.Wait()                          // poll accepted, now blocked in the convergence timer
		time.Sleep(goldenOrder().Success.Window) // fake clock jumps the convergence window
		synctest.Wait()                          // convergence goroutine finishes its post-window work

		if n := len(tr.Log.ByResult(outcome.ResultPartialNonConverging)); n != 0 {
			t.Errorf("an accepted release must not also record partial_non_converging, got %d", n)
		}
		success := tr.Log.ByResult(outcome.ResultSuccess)
		if len(success) != 1 {
			t.Fatalf("want exactly one success outcome once accepted and converged, got %d", len(success))
		}
	})
}

func TestWatchAndAccept_RecordsFailureWhenTheProbeItselfErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inbox, outbox := t.TempDir(), t.TempDir()
		writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())

		runner := &fakeRunner{result: outcome.ResultProposed}
		tr := newTestTransport(inbox, outbox)
		tr.Exec = thump.Live{Runner: runner}
		tr.Acceptance = &thump.AcceptanceWatcher{Probe: &fakeReleaseProbe{err: errors.New("forge unreachable")}}

		if err := tr.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(goldenOrder().Success.Window)
		synctest.Wait()

		failed := tr.Log.ByResult(outcome.ResultFailure)
		if len(failed) != 1 {
			t.Fatalf("want exactly one failure outcome for a probe error, got %d", len(failed))
		}
		if got := failed[0].Error; got == "" {
			t.Error("a probe error must carry its error text, got none")
		}
	})
}

func TestWatchAndAccept_RecordsNothingWhenCtxIsCancelledMidPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inbox, outbox := t.TempDir(), t.TempDir()
		writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())

		runner := &fakeRunner{result: outcome.ResultProposed}
		tr := newTestTransport(inbox, outbox)
		tr.Exec = thump.Live{Runner: runner}
		tr.Acceptance = &thump.AcceptanceWatcher{Probe: &fakeReleaseProbe{accepted: true}}

		ctx, cancel := context.WithCancel(context.Background())
		if err := tr.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait() // poll goroutine reaches its timer block
		cancel()        // shutdown mid-poll, before the acceptance window elapses
		synctest.Wait() // poll goroutine observes ctx.Done and returns

		if n := len(tr.Log.ByResult(outcome.ResultPartialNonConverging)); n != 0 {
			t.Errorf("ctx-cancelled poll recorded %d partial_non_converging outcomes, want 0", n)
		}
		if n := len(tr.Log.ByResult(outcome.ResultSuccess)); n != 0 {
			t.Errorf("ctx-cancelled poll recorded %d success outcomes, want 0", n)
		}
		if n := len(tr.Log.ByResult(outcome.ResultFailure)); n != 0 {
			t.Errorf("ctx-cancelled poll recorded %d failure outcomes, want 0", n)
		}
	})
}
