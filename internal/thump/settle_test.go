package thump_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

func TestWatchAndSettle_EmitsAConvergenceOutcomeAfterTheWindow(t *testing.T) {
	cases := map[string]struct {
		converges    bool
		wantResult   outcome.Result
		wantReversal bool
	}{
		"watchAndSettle records success when the SLO recovers in the window": {
			converges: true, wantResult: outcome.ResultSuccess, wantReversal: false,
		},
		"watchAndSettle records partial_non_converging and reverses when the SLO keeps burning": {
			converges: false, wantResult: outcome.ResultPartialNonConverging, wantReversal: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				inbox, outbox := t.TempDir(), t.TempDir()
				writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())

				runner := &fakeRunner{}
				tr := newTestTransport(inbox, outbox)
				tr.Exec = thump.Live{Runner: runner}
				tr.Reversal = &thump.ReversalWatcher{
					Probe: thump.PrometheusConverger{Probe: &fakeProbe{
						answer: tc.converges, severity: 0.8, severityOK: true,
					}},
					Now: frozenNow,
				}

				if err := tr.Tick(context.Background()); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()                          // settle goroutine reaches its timer block
				time.Sleep(goldenOrder().Success.Window) // fake clock jumps the window
				synctest.Wait()                          // settle goroutine finishes its post-window work

				// G1: read tr.Log, never the outbox — applied + convergence share a filename.
				conv := tr.Log.ByResult(tc.wantResult)
				if len(conv) != 1 {
					t.Fatalf("want exactly one %s outcome, got %d", tc.wantResult, len(conv))
				}
				if conv[0].ObservedSeverity == nil || *conv[0].ObservedSeverity != 0.8 {
					t.Errorf("convergence outcome must carry the normalized severity, got %v", conv[0].ObservedSeverity)
				}
				// The forward order always records one applied outcome first. A
				// fired reversal is itself just another clean live run (Live.Execute
				// is uniform across forward and undo — internal/thump/live.go), so
				// it records a second applied outcome; nothing ever watches the
				// reversal's own convergence, so that second one stays a dangling
				// interim record — harmless, because the forward order's own
				// convergence outcome already closed the ledger set.
				wantApplied := 1
				if tc.wantReversal {
					wantApplied = 2
				}
				if n := len(tr.Log.ByResult(outcome.ResultApplied)); n != wantApplied {
					t.Errorf("applied outcome count = %d, want %d", n, wantApplied)
				}
				if runner.gotReverse != tc.wantReversal {
					t.Errorf("reversal fired = %v, want %v", runner.gotReverse, tc.wantReversal)
				}
			})
		})
	}
}
