package thump_test

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

func TestWatch_FiresTheUndoOnALossOrOnAnAuthoredRestore(t *testing.T) {
	cases := map[string]struct {
		probe            thump.Converger
		restoreOnSuccess bool
		wantConverged    bool
		wantFire         bool
	}{
		"Watch fires the undo after a met window when the contract authored a restore": {
			probe: alwaysConverges{}, restoreOnSuccess: true,
			wantConverged: true, wantFire: true,
		},
		"Watch leaves a met window alone when no restore is authored": {
			probe: alwaysConverges{}, restoreOnSuccess: false,
			wantConverged: true, wantFire: false,
		},
		"Watch fires the reversal after an unmet window whatever the restore flag says": {
			probe: neverConverges{}, restoreOnSuccess: false,
			wantConverged: false, wantFire: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := goldenOrder()
				o.Reversal.RestoreOnSuccess = tc.restoreOnSuccess
				w := thump.ReversalWatcher{Probe: tc.probe, Now: frozenNow}

				got := w.Watch(context.Background(), o)

				if diff := cmp.Diff(tc.wantConverged, got.Converged); diff != "" {
					t.Error("wrong convergence verdict", diff)
				}
				if diff := cmp.Diff(tc.wantFire, got.Fire); diff != "" {
					t.Error("wrong undo decision", diff)
				}
				if tc.wantFire && got.Undo.Kind != thump.OrderReversal {
					t.Errorf("an undo must be Kind=%q so a disarmed kill-switch still lets it through, got %q",
						thump.OrderReversal, got.Undo.Kind)
				}
			})
		})
	}
}

func TestWatch_RendersTheUndoOrderFromTheForwardsAuthoredReversal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := thump.ReversalWatcher{Probe: neverConverges{}, Now: frozenNow}

		got := w.Watch(context.Background(), goldenOrder())

		if got.Severity == nil || *got.Severity != 0.9 {
			t.Errorf("Watch must hand back the probe's severity reading, got %v", got.Severity)
		}
		want := thump.Order{
			ID:          "rev:slo_burn:ceph-rgw:1000",
			Kind:        thump.OrderReversal,
			DecisionRef: "dec:slo_burn:ceph-rgw:1000",
			SignalRef:   "slo_burn:ceph-rgw",
			ContractRef: "throttle-non-critical-paths",
			Description: "unthrottle", // the forward order's authored reversal.method, now the thing to run
			Reversal:    goldenOrder().Reversal,
			RenderedAt:  frozenNow(),
		}
		if diff := cmp.Diff(want, got.Undo); diff != "" {
			t.Error("undo order drifted from the golden fixture (-want +got)", diff)
		}
	})
}

func TestWatch_HandsBackTheProbesSeverityReadingEvenWhenConverged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := thump.ReversalWatcher{Probe: alwaysConverges{}, Now: frozenNow}

		got := w.Watch(context.Background(), goldenOrder())

		if got.Severity == nil || *got.Severity != 0.05 {
			t.Errorf("Watch must hand back the probe's severity reading even when converged, got %v", got.Severity)
		}
	})
}

func TestReversalWatcher_AReversalSurvivesADisarmedKillSwitch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := thump.ReversalWatcher{Probe: neverConverges{}, Now: frozenNow}
		settlement := w.Watch(context.Background(), goldenOrder())
		if !settlement.Fire {
			t.Fatal("setup: expected a reversal to fire")
		}

		spy := &spyExecutor{inner: thump.DryRun{}}
		gated := thump.GatedExecutor{Inner: spy, Switch: fakeSwitch(false)} // disarmed

		got := gated.Execute(context.Background(), settlement.Undo, frozenNow())

		if !spy.called {
			t.Error("a disarmed kill-switch must still let an approved reversal through — blocking cleanup strands infrastructure half-changed")
		}
		if got.Result != outcome.ResultRendered {
			t.Errorf("reversal outcome result = %q, want %q (executed, not blocked)", got.Result, outcome.ResultRendered)
		}
	})
}

// neverConverges / alwaysConverges are the two poles of the convergence probe
// — a real Converger reads telemetry, but the reversal decision only turns on
// its bool, so the poles are the whole test surface.
type neverConverges struct{}

func (neverConverges) Settle(context.Context, thump.Order) (bool, *float64) { return false, new(0.9) }

type alwaysConverges struct{}

func (alwaysConverges) Settle(context.Context, thump.Order) (bool, *float64) { return true, new(0.05) }
