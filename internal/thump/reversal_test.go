package thump_test

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

// neverConverges / alwaysConverges are the two poles of the convergence probe
// — a real Converger reads telemetry, but the reversal decision only turns on
// its bool, so the poles are the whole test surface.
type neverConverges struct{}

func (neverConverges) Converged(context.Context, thump.Order) bool { return false }

type alwaysConverges struct{}

func (alwaysConverges) Converged(context.Context, thump.Order) bool { return true }

func TestReversalWatcher_FiresAReversalWhenTheWindowElapsesWithCriteriaUnmet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := thump.ReversalWatcher{Probe: neverConverges{}, Now: frozenNow}

		got, fired := w.Watch(context.Background(), goldenOrder())

		if !fired {
			t.Fatal("an unmet success window must fire a reversal")
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
		if diff := cmp.Diff(want, got); diff != "" {
			t.Error("reversal order drifted from the golden fixture (-want +got)", diff)
		}
	})
}

func TestReversalWatcher_HoldsWhenTheCriteriaAreMet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := thump.ReversalWatcher{Probe: alwaysConverges{}, Now: frozenNow}

		got, fired := w.Watch(context.Background(), goldenOrder())

		if fired {
			t.Errorf("a met success window must fire no reversal, got %+v", got)
		}
	})
}

func TestReversalWatcher_AReversalSurvivesADisarmedKillSwitch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := thump.ReversalWatcher{Probe: neverConverges{}, Now: frozenNow}
		reversal, fired := w.Watch(context.Background(), goldenOrder())
		if !fired {
			t.Fatal("setup: expected a reversal to fire")
		}

		spy := &spyExecutor{inner: thump.DryRun{}}
		gated := thump.GatedExecutor{Inner: spy, Switch: fakeSwitch(false)} // disarmed

		got := gated.Execute(context.Background(), reversal, frozenNow())

		if !spy.called {
			t.Error("a disarmed kill-switch must still let an approved reversal through — blocking cleanup strands infrastructure half-changed")
		}
		if got.Result != outcome.ResultRendered {
			t.Errorf("reversal outcome result = %q, want %q (executed, not blocked)", got.Result, outcome.ResultRendered)
		}
	})
}
