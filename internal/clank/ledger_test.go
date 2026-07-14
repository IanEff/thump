package clank_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

func TestProposalLog_OpenRespectsTheDedupeWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := clank.NewMemProposalLog()
	at := time.Now()
	if err := log.Record(ctx, proposal.Set{SignalRef: "fp-1", Status: &proposal.Status{Phase: "proposed"}}); err != nil {
		t.Fatal(err)
	}

	in, err := log.Open(ctx, "fp-1", at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 {
		t.Errorf("recorded in-window should be open: want 1, got %d", len(in))
	}
	out, err := log.Open(ctx, "fp-1", at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("older than `since` should not be open: want 0, got %d", len(out))
	}
}

func TestProposalLog_OpenIgnoresClosedSets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := clank.NewMemProposalLog()
	if err := log.Record(ctx, proposal.Set{
		SignalRef: "fp-1",
		Status:    &proposal.Status{Phase: "closed"},
	}); err != nil {
		t.Fatal(err)
	}
	open, err := log.Open(ctx, "fp-1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("a closed set must not suppress a new one: want 0, got %d", len(open))
	}
}

func TestObserve_TheOutcomeTransitionTable(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		in          outcome.Outcome
		wantPhase   string
		wantOutcome string
	}{
		// a rehearsal acknowledges the set — the machine took it seriously
		// end-to-end — but closes nothing: nothing acted, so there is no
		// outcome to record, and the set STAYS OPEN (it keeps deduping —
		// the incident it describes was never addressed).
		"Observe marks a rehearsed set acknowledged and keeps it open": {
			in: rehearsal(), wantPhase: proposal.PhaseAcknowledge, wantOutcome: "",
		},
		// live terminals close the loop, both directions — a recorded
		// failure is exactly as valuable as a success (it's half of every
		// honest success RATE).
		"Observe closes a set whose live act succeeded": {
			in: liveSuccess(), wantPhase: proposal.PhaseClosed, wantOutcome: "success",
		},
		"Observe closes a set whose live act failed": {
			in: liveFailure(), wantPhase: proposal.PhaseClosed, wantOutcome: "failure",
		},
		// the unsettled pair stays ACTED — in-flight, not resolved. This is
		// I-6 defence 4 cashing out in the lifecycle: a vocabulary that had
		// to round "half-worked and not settling" to success or failure
		// would close this set as one of the lies.
		"Observe leaves a set acted when the live outcome is unknown": {
			in: liveUnknown(), wantPhase: proposal.PhaseActed, wantOutcome: "unknown",
		},
		"Observe leaves a partial non-converging set acted and in-flight": {
			in: livePartial(), wantPhase: proposal.PhaseActed, wantOutcome: "partial_non_converging",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			l := seededLedger(t) // one clickSet() recorded, phase proposed

			got, err := l.Observe(context.Background(), tc.in)
			if err != nil {
				t.Fatal("a matching open set must observe cleanly:", err)
			}

			if diff := cmp.Diff(tc.wantPhase, got.Status.Phase); diff != "" {
				t.Error("wrong phase (-want +got)", diff)
			}
			if diff := cmp.Diff(tc.wantOutcome, got.Status.Outcome); diff != "" {
				t.Error("wrong outcome (-want +got)", diff)
			}
			if diff := cmp.Diff(tc.in.ExecutedAt, got.Status.ObservedAt); diff != "" {
				t.Error("ObservedAt must be the outcome's own timestamp — click has no clock (-want +got)", diff)
			}
		})
	}
}

func TestLedger_DeclineClosesTheDedupWindow(t *testing.T) {
	t.Parallel()
	l := seededLedger(t) // one clickSet() recorded, phase proposed
	declinedAt := time.Unix(2000, 0)

	got, err := l.Decline(context.Background(), "slo_burn:ceph-rgw", declinedAt)
	if err != nil {
		t.Fatal("a matching open set must decline cleanly:", err)
	}

	if diff := cmp.Diff(proposal.PhaseDeclined, got.Status.Phase); diff != "" {
		t.Error("wrong phase (-want +got)", diff)
	}
	if diff := cmp.Diff(declinedAt, got.Status.ObservedAt); diff != "" {
		t.Error("ObservedAt must be the decision's own EvaluatedAt (-want +got)", diff)
	}
	if got.Status.Outcome != "" {
		t.Errorf("a decline never touches Status.Outcome — nothing was executed, got %q", got.Status.Outcome)
	}

	// the whole point: a fresh detection on the same fingerprint is no
	// longer suppressed, well inside what would have been the DedupeWindow.
	open, err := l.Open(context.Background(), "slo_burn:ceph-rgw", declinedAt.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("a declined set must not suppress a new one: want 0, got %d", len(open))
	}
}

func TestLedger_DeclineReturnsErrNoOpenSetWhenNothingMatches(t *testing.T) {
	t.Parallel()
	l := clank.NewMemProposalLog() // empty — nobody proposed anything

	_, err := l.Decline(context.Background(), "slo_burn:ceph-rgw", time.Unix(2000, 0))

	if !errors.Is(err, clank.ErrNoOpenSet) {
		t.Errorf("a decline with no open set to answer to must earn ErrNoOpenSet, got %v", err)
	}
}

func TestObserve_AnOrphanOutcomeIsANamedRefusal(t *testing.T) {
	t.Parallel()
	l := clank.NewMemProposalLog() // empty — nobody proposed anything

	_, err := l.Observe(context.Background(), liveSuccess())

	if !errors.Is(err, clank.ErrNoOpenSet) {
		t.Errorf("an outcome nothing proposed must earn ErrNoOpenSet, got %v", err)
	}
}

func TestObserve_TouchesStatusAndNothingElse(t *testing.T) {
	t.Parallel()
	l := clank.NewMemProposalLog()
	set := clickSet()
	callerView := set.Status // the pointer the engine's caller (and the sink) still hold
	if err := l.Record(context.Background(), set); err != nil {
		t.Fatal(err)
	}

	got, err := l.Observe(context.Background(), liveSuccess())
	if err != nil {
		t.Fatal("a matching open set must observe cleanly:", err)
	}

	// everything except Status crossed the transition untouched:
	want := clickSet()
	want.Status = &proposal.Status{
		Phase:      proposal.PhaseClosed,
		Outcome:    "success",
		ObservedAt: liveSuccess().ExecutedAt,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("Observe may transition Status and NOTHING else (-want +got)", diff)
	}
	// …and the caller's aliased pointer was not written through — their
	// snapshot of history still says what it said when they took it:
	if diff := cmp.Diff(proposal.PhaseProposed, callerView.Phase); diff != "" {
		t.Error("Observe wrote through the shared Status pointer — history changed retroactively (-want +got)", diff)
	}
}

func liveSuccess() outcome.Outcome {
	return outcome.Outcome{
		ID:          "out:slo_burn:ceph-rgw:1000", // thump's stamp: "out:" + SignalRef + ":" + unix
		DecisionRef: "dec:slo_burn:ceph-rgw:1000", // hiss's stamp — the audit chain holds
		SignalRef:   "slo_burn:ceph-rgw",          // == clickSet().SignalRef — Observe's join key
		ContractRef: "throttle-non-critical-paths",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultSuccess,
		ExecutedAt:  time.Unix(1000, 0), // the house instant
	}
}

func rehearsal() outcome.Outcome {
	o := liveSuccess()
	o.Mode, o.Result = outcome.ModeDryRun, outcome.ResultRendered
	return o
}

func liveFailure() outcome.Outcome {
	o := liveSuccess()
	o.Result, o.Error = outcome.ResultFailure, "throttle applied; latency did not recover"
	return o
}

func liveUnknown() outcome.Outcome {
	o := liveSuccess()
	o.Result = outcome.ResultUnknown
	return o
}

func livePartial() outcome.Outcome {
	o := liveSuccess() // Auditable demands failure/partial explain themselves —
	o.Result, o.Error = outcome.ResultPartialNonConverging, "latency recovered; error rate did not"
	return o
}

func seededLedger(t *testing.T) *clank.MemProposalLog {
	t.Helper()
	l := clank.NewMemProposalLog()
	if err := l.Record(context.Background(), clickSet()); err != nil {
		t.Fatal(err)
	}
	return l
}

func clickSet() proposal.Set {
	return proposal.Set{
		Name:         "ps-ceph-rgw-001",
		SignalRef:    "slo_burn:ceph-rgw", // rattle's fingerprint — the case base's key
		FailureClass: proposal.ClassDependencySaturation,
		ServiceTier:  "tier-1",
		Gate: &clank.GateResult{ // recorded sets that produced outcomes were gated — the fixture models reality
			BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true,
		},
		Proposals: []proposal.Candidate{{
			ID: "p1", ContractRef: "throttle-non-critical-paths",
			Confidence: 0.87, // → Case.Confidence — the STATED half of CE's pair
			Rank:       1,
		}},
		Recommended: "p1",
		Status:      &proposal.Status{Phase: proposal.PhaseProposed}, // engine.go:42,140
	}
}
