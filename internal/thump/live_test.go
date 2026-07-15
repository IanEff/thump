package thump_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

// fakeRunner is a scriptable ActionRunner: it records the (ref, reverse,
// params) it was dispatched with — so a test can prove Live handed it the
// Order's own fields — and returns a programmable error to drive the
// success/failure branches.
type fakeRunner struct {
	called     bool
	gotRef     string
	gotReverse bool
	gotParams  map[string]float64
	err        error
}

func (r *fakeRunner) Run(_ context.Context, ref string, reverse bool, params map[string]float64) error {
	r.called = true
	r.gotRef = ref
	r.gotReverse = reverse
	r.gotParams = params
	return r.err
}

func TestLive_RunsTheForwardActionAndRecordsAnAuditableSuccess(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	live := thump.Live{Runner: runner}

	got := live.Execute(context.Background(), goldenOrder(), frozenNow())

	if !runner.called {
		t.Fatal("Live must delegate to its ActionRunner")
	}
	if runner.gotReverse {
		t.Error("a forward order must run the action, not its undo (reverse=false)")
	}
	if runner.gotRef != "throttle-non-critical-paths" {
		t.Errorf("runner ref = %q, want the order's ContractRef", runner.gotRef)
	}
	if diff := cmp.Diff(map[string]float64{"throttle_pct": 25}, runner.gotParams); diff != "" {
		t.Error("runner params drifted from the order (-want +got)", diff)
	}
	if err := got.Auditable(); err != nil {
		t.Fatal("a live outcome must be auditable:", err)
	}
	want := outcome.Outcome{
		ID:          "out:slo_burn:ceph-rgw:1000",
		DecisionRef: "dec:slo_burn:ceph-rgw:1000",
		SignalRef:   "slo_burn:ceph-rgw",
		ContractRef: "throttle-non-critical-paths",
		Mode:        outcome.ModeLive,      // it acted…
		Result:      outcome.ResultSuccess, // …and the action landed
		ExecutedAt:  frozenNow(),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("live success outcome drifted from the golden fixture (-want +got)", diff)
	}
}

func TestLive_RecordsAFailureWithErrorTextWhenTheRunnerFails(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{err: errors.New("kubectl: connection refused")}
	live := thump.Live{Runner: runner}

	got := live.Execute(context.Background(), goldenOrder(), frozenNow())

	if got.Mode != outcome.ModeLive {
		t.Errorf("outcome mode = %q, want %q", got.Mode, outcome.ModeLive)
	}
	if got.Result != outcome.ResultFailure {
		t.Errorf("outcome result = %q, want %q", got.Result, outcome.ResultFailure)
	}
	if !strings.Contains(got.Error, "connection refused") {
		t.Errorf("outcome error = %q, want it to carry the runner's failure", got.Error)
	}
	if err := got.Auditable(); err != nil {
		t.Fatal("a failure carrying its error text is accountability, and must be auditable:", err)
	}
}

func TestLive_RunsTheAuthoredUndoForAReversalOrder(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	live := thump.Live{Runner: runner}

	order := goldenOrder()
	order.Kind = thump.OrderReversal

	live.Execute(context.Background(), order, frozenNow())

	if !runner.gotReverse {
		t.Error("a reversal order must run the authored undo (reverse=true), not the forward action")
	}
}

// TestLive_ComposesUnderADisarmedKillSwitch ties the new executor to the
// Wave-0 gate: a disarmed switch blocks a forward Live order before the runner
// is ever reached, but exempts a reversal so in-flight cleanup still lands.
func TestLive_ComposesUnderADisarmedKillSwitch(t *testing.T) {
	t.Parallel()

	fwdRunner := &fakeRunner{}
	fwd := thump.GatedExecutor{Inner: thump.Live{Runner: fwdRunner}, Switch: fakeSwitch(false)}.
		Execute(context.Background(), goldenOrder(), frozenNow())
	if fwdRunner.called {
		t.Error("a disarmed switch must block a forward order before it reaches the runner")
	}
	if fwd.Result != outcome.ResultBlocked {
		t.Errorf("blocked forward result = %q, want %q", fwd.Result, outcome.ResultBlocked)
	}

	revRunner := &fakeRunner{}
	revOrder := goldenOrder()
	revOrder.Kind = thump.OrderReversal
	rev := thump.GatedExecutor{Inner: thump.Live{Runner: revRunner}, Switch: fakeSwitch(false)}.
		Execute(context.Background(), revOrder, frozenNow())
	if !revRunner.called {
		t.Error("a disarmed switch must still let a reversal reach the runner — blocking cleanup strands infrastructure half-changed")
	}
	if !revRunner.gotReverse {
		t.Error("the exempted reversal must run the undo (reverse=true)")
	}
	if rev.Result != outcome.ResultSuccess {
		t.Errorf("exempted reversal result = %q, want %q", rev.Result, outcome.ResultSuccess)
	}
}
