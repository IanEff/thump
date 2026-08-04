package harvest_test

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/harvest"
)

func TestSettle_ReturnsOnTheSettledOutcomeAndNotOnTheExecutorsAck(t *testing.T) {
	t.Parallel()
	// A live action publishes applied the moment the mutation runs, minutes
	// before the convergence watcher decides whether it worked. A harvest that
	// stops there mines the ack instead of the result and records every
	// incident as a win.
	cases := map[string]struct {
		feed []outcome.Result
		want outcome.Result
	}{
		"Settle skips applied and returns the success that supersedes it": {
			feed: []outcome.Result{outcome.ResultApplied, outcome.ResultSuccess},
			want: outcome.ResultSuccess,
		},
		"Settle returns partial_non_converging as a terminal result": {
			feed: []outcome.Result{outcome.ResultApplied, outcome.ResultPartialNonConverging},
			want: outcome.ResultPartialNonConverging,
		},
		"Settle returns blocked without waiting for a convergence that will never run": {
			feed: []outcome.Result{outcome.ResultBlocked},
			want: outcome.ResultBlocked,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := harvest.Settle(t.Context(), feedWatcher(tc.feed), "slo_burn:ceph-cluster", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got.Result); diff != "" {
				t.Error("settled on the wrong outcome", diff)
			}
		})
	}
}

func TestSettle_ReportsTheTimeoutRatherThanWaitingForever(t *testing.T) {
	t.Parallel()
	// An incident that never settles is a finding. Blocking forever turns it
	// into an operator staring at a terminal, which is the cost this whole
	// track exists to remove.
	synctest.Test(t, func(t *testing.T) {
		_, err := harvest.Settle(t.Context(), silentWatcher{}, "slo_burn:ceph-cluster", 20*time.Minute)
		if !errors.Is(err, harvest.ErrSettleTimeout) {
			t.Error("want ErrSettleTimeout", err)
		}
	})
}
