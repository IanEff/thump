package harvest_test

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/harvest"
)

const testSignalRef = "slo_burn:ceph-cluster"

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
			legs := harvest.Legs{Outcomes: feedWatcher(tc.feed)}
			got, err := harvest.Settle(t.Context(), legs, testSignalRef, time.Minute, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff("approved", got.Verdict); diff != "" {
				t.Error("wrong verdict for a settled outcome", diff)
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
		legs := harvest.Legs{Outcomes: silentWatcher{}}
		_, err := harvest.Settle(t.Context(), legs, testSignalRef, 20*time.Minute, 3*time.Minute)
		if !errors.Is(err, harvest.ErrSettleTimeout) {
			t.Error("want ErrSettleTimeout", err)
		}
	})
}

func TestSettle_NoLegFiringAtAllStaysATimeoutDistinctFromARefusal(t *testing.T) {
	t.Parallel()
	// Every leg nil (no watcher wired at all) must still read as a timeout —
	// the same record a dead rig produces, never confused with a refusal,
	// which requires a detection to have actually arrived.
	synctest.Test(t, func(t *testing.T) {
		_, err := harvest.Settle(t.Context(), harvest.Legs{}, testSignalRef, 20*time.Minute, 3*time.Minute)
		if !errors.Is(err, harvest.ErrSettleTimeout) {
			t.Error("want ErrSettleTimeout", err)
		}
	})
}

func TestSettle_ReturnsDeclinedForAMatchingDecline(t *testing.T) {
	t.Parallel()
	legs := harvest.Legs{
		Declines: feedDeclineWatcher{{ID: "dec:1", SignalRef: testSignalRef, Verdict: decision.VerdictRejected}},
	}
	got, err := harvest.Settle(t.Context(), legs, testSignalRef, time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := harvest.Terminal{Verdict: "declined", DecisionRef: "dec:1"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong terminal for a matching decline", diff)
	}
}

func TestSettle_ReturnsHeldForAMatchingHold(t *testing.T) {
	t.Parallel()
	g := decision.Governed{
		Decision: decision.Decision{ID: "dec:2", SignalRef: testSignalRef, CandidateRef: "cand:1", Verdict: decision.VerdictHold},
		Set: proposal.Set{
			SignalRef: testSignalRef,
			Proposals: []proposal.Candidate{{ID: "cand:1", ContractRef: "accelerate-recovery"}},
		},
	}
	legs := harvest.Legs{Held: feedHeldWatcher{g}}
	got, err := harvest.Settle(t.Context(), legs, testSignalRef, time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := harvest.Terminal{Verdict: "held", ContractRef: "accelerate-recovery", DecisionRef: "dec:2"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("wrong terminal for a matching hold", diff)
	}
}

func TestSettle_AnOutcomeStillWinsAlongsideUnrelatedTrafficOnOtherLegs(t *testing.T) {
	t.Parallel()
	legs := harvest.Legs{
		Outcomes: feedWatcher{outcome.ResultSuccess},
		Declines: feedDeclineWatcher{{ID: "dec:other", SignalRef: "slo_burn:unrelated"}},
	}
	got, err := harvest.Settle(t.Context(), legs, testSignalRef, time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("approved", got.Verdict); diff != "" {
		t.Error("an unrelated decline on another fingerprint must not shadow the real outcome", diff)
	}
}

func TestSettle_RefusesWhenADetectionArrivesWithNoProposalInsideTheGrace(t *testing.T) {
	t.Parallel()
	// A clank refusal publishes nothing — the only way to see it is the
	// detection that should have produced a Set and never did.
	synctest.Test(t, func(t *testing.T) {
		legs := harvest.Legs{
			Detections: feedDetectionWatcher{{Fingerprint: testSignalRef}},
			Sets:       feedSetWatcher(nil),
		}
		got, err := harvest.Settle(t.Context(), legs, testSignalRef, 10*time.Minute, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff("refused", got.Verdict); diff != "" {
			t.Error("wrong verdict when a detection's grace elapses with no proposal", diff)
		}
	})
}

func TestSettle_ASetArrivingInsideTheGraceCancelsTheRefusalAndTheNormalWaitResumes(t *testing.T) {
	t.Parallel()
	legs := harvest.Legs{
		Detections: feedDetectionWatcher{{Fingerprint: testSignalRef}},
		Sets:       feedSetWatcher{{SignalRef: testSignalRef}},
		Outcomes:   feedWatcher{outcome.ResultSuccess},
	}
	got, err := harvest.Settle(t.Context(), legs, testSignalRef, time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("approved", got.Verdict); diff != "" {
		t.Error("a Set inside the grace should cancel the refusal, letting the outcome win", diff)
	}
}
