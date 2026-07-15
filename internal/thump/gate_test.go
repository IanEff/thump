package thump_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/thump"
)

// fakeSwitch is a KillSwitch pinned to one arm state — the switch is a single
// binary by design, so a bool is the whole fake.
type fakeSwitch bool

func (s fakeSwitch) Armed(context.Context) bool { return bool(s) }

// spyExecutor records whether Inner was reached, so a test can prove a block
// short-circuits before delegation rather than merely returning a blocked-
// looking Outcome after the fact.
type spyExecutor struct {
	inner  thump.Executor
	called bool
}

func (s *spyExecutor) Execute(ctx context.Context, o thump.Order, now time.Time) outcome.Outcome {
	s.called = true
	return s.inner.Execute(ctx, o, now)
}

func TestGatedExecutor_GatesForwardOrdersButExemptsReversals(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		armed       bool
		kind        thump.OrderKind
		wantReached bool           // did the call reach the inner executor?
		wantResult  outcome.Result // ...and what terminal state came back
	}{
		"delegates a forward order when the switch is armed": {
			armed: true, kind: thump.OrderForward,
			wantReached: true, wantResult: outcome.ResultRendered,
		},
		"blocks a forward order when the switch is disarmed": {
			armed: false, kind: thump.OrderForward,
			wantReached: false, wantResult: outcome.ResultBlocked,
		},
		"exempts a reversal order when the switch is disarmed": {
			armed: false, kind: thump.OrderReversal,
			wantReached: true, wantResult: outcome.ResultRendered,
		},
		"delegates a reversal order when the switch is armed": {
			armed: true, kind: thump.OrderReversal,
			wantReached: true, wantResult: outcome.ResultRendered,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			spy := &spyExecutor{inner: thump.DryRun{}}
			gated := thump.GatedExecutor{Inner: spy, Switch: fakeSwitch(tc.armed)}

			order := goldenOrder()
			order.Kind = tc.kind

			got := gated.Execute(context.Background(), order, frozenNow())

			if spy.called != tc.wantReached {
				t.Errorf("reached inner executor = %v, want %v", spy.called, tc.wantReached)
			}
			if got.Result != tc.wantResult {
				t.Errorf("outcome result = %q, want %q", got.Result, tc.wantResult)
			}
			if err := got.Auditable(); err != nil {
				t.Fatal("every gated outcome must be auditable:", err)
			}
		})
	}
}

func TestGatedExecutor_RecordsAnAuditableBlockedOutcome(t *testing.T) {
	t.Parallel()
	spy := &spyExecutor{inner: thump.DryRun{}}
	gated := thump.GatedExecutor{Inner: spy, Switch: fakeSwitch(false)}

	got := gated.Execute(context.Background(), goldenOrder(), frozenNow())

	if spy.called {
		t.Error("a blocked order must never reach the inner executor")
	}
	if err := got.Auditable(); err != nil {
		t.Fatal("a block is a record, not silence — it must be auditable:", err)
	}
	want := outcome.Outcome{
		ID:          "out:slo_burn:ceph-rgw:1000",
		DecisionRef: "dec:slo_burn:ceph-rgw:1000",
		SignalRef:   "slo_burn:ceph-rgw",
		ContractRef: "throttle-non-critical-paths",
		Mode:        outcome.ModeLive,      // the block is a fact about the live path…
		Result:      outcome.ResultBlocked, // …a refusal, not a rehearsal and not a failure
		ExecutedAt:  frozenNow(),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("blocked outcome drifted from the golden fixture (-want +got)", diff)
	}
}
