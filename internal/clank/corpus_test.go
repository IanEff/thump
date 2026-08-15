package clank_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

func caseAt(confidence float64, result outcome.Result) clank.Case {
	return clank.Case{
		FailureClass: proposal.ClassRedundancyDegraded,
		Confidence:   confidence,
		Result:       result,
	}
}

func makeCorpus(t *testing.T, cases ...clank.Case) clank.Corpus {
	t.Helper()
	return clank.Corpus{Cases: cases, MinedAt: time.Now()}
}

func TestFloorSupport_CountsARefusedSuccessAsEvidenceTheFloorIsTooHigh(t *testing.T) {
	t.Parallel()
	// A floor is only defensible if the runs it refused would have failed.
	// The seeded values in config/hiss/policy.yaml have never been asked
	// this question at all: redundancy_degraded sits at 0.3 because one run
	// needed to get through, and nothing since has checked what 0.3 admits
	// or what 0.75 would have refused.
	cases := map[string]struct {
		corpus clank.Corpus
		floor  float64
		want   clank.FloorSupport
	}{
		"FloorSupport counts a success below the floor as a refused win": {
			corpus: makeCorpus(t,
				caseAt(0.87, outcome.ResultSuccess),
				caseAt(0.61, outcome.ResultSuccess),
			),
			floor: 0.75,
			want: clank.FloorSupport{
				Class: proposal.ClassRedundancyDegraded, Floor: 0.75,
				AdmittedTotal: 1, AdmittedWins: 1,
				RefusedTotal: 1, RefusedWins: 1,
			},
		},
		"FloorSupport counts a partial_non_converging as a miss, not a win": {
			// I-6 defence 4: binary success/failure is the belief-formation
			// trap, and recordCalibration (metrics.go:105) already reads
			// partial as a miss. The corpus does not get to disagree.
			corpus: makeCorpus(t, caseAt(0.87, outcome.ResultPartialNonConverging)),
			floor:  0.75,
			want: clank.FloorSupport{
				Class: proposal.ClassRedundancyDegraded, Floor: 0.75,
				AdmittedTotal: 1, AdmittedWins: 0,
			},
		},
		"FloorSupport ignores a dry-run case entirely rather than scoring it zero": {
			// A rendered outcome is not a failure — it is the absence of a
			// result, and counting it as a miss would make every dry-run
			// session argue for a higher floor.
			corpus: makeCorpus(t, caseAt(0.87, outcome.ResultRendered)),
			floor:  0.75,
			want:   clank.FloorSupport{Class: proposal.ClassRedundancyDegraded, Floor: 0.75},
		},
		"FloorSupport ignores an applied-but-not-yet-settled case, same as a dry-run": {
			// MineCorpus joins a Set to every outcome record published for its
			// SignalRef, not just the final terminal one — an incident that
			// executes and later converges shows up twice, once as "applied"
			// (execute-time, not one of Success/PartialNonConverging/Failure)
			// and once with its real settled Result. Scoring "applied" as a
			// miss double-counted every executed incident against the floor
			// it cleared to run at all.
			corpus: makeCorpus(t, caseAt(0.87, outcome.ResultApplied)),
			floor:  0.75,
			want:   clank.FloorSupport{Class: proposal.ClassRedundancyDegraded, Floor: 0.75},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := tc.corpus.FloorSupport(proposal.ClassRedundancyDegraded, tc.floor)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("floor support misreads the corpus", diff)
			}
		})
	}
}

func TestMineCorpus_JoinsAnOutcomeToTheSetOpenWhenItSettled(t *testing.T) {
	t.Parallel()
	// A signal can fire twice (a re-detection after the first set closed).
	// MemProposalLog.Observe matches an outcome to whichever set was open at
	// the time; offline, the WAL holds no "open" flag to read back, so the
	// join has to reconstruct it from timestamps — the most recent set whose
	// Status closed no later than the outcome's ExecutedAt.
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	older := proposal.Set{
		SignalRef:   "fp:cephblockpool",
		Status:      &proposal.Status{ObservedAt: base},
		Proposals:   []proposal.Candidate{{ID: "c1", Confidence: 0.61}},
		Recommended: "c1",
	}
	newer := proposal.Set{
		SignalRef:   "fp:cephblockpool",
		Status:      &proposal.Status{ObservedAt: base.Add(time.Hour)},
		Proposals:   []proposal.Candidate{{ID: "c2", Confidence: 0.87}},
		Recommended: "c2",
	}
	o := outcome.Outcome{
		SignalRef:  "fp:cephblockpool",
		Result:     outcome.ResultSuccess,
		ExecutedAt: base.Add(2 * time.Hour),
	}

	got := clank.MineCorpus([]proposal.Set{older, newer}, []outcome.Outcome{o})

	want := []clank.Case{{
		SignalRef:    "fp:cephblockpool",
		FailureClass: "",
		Confidence:   0.87,
		Result:       outcome.ResultSuccess,
		ObservedAt:   o.ExecutedAt,
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("mined the wrong set for this outcome", diff)
	}
}

func TestMineCorpus_CarriesTheJoinedSetsRunIDOntoTheCase(t *testing.T) {
	t.Parallel()
	// RunID is the only thing that joins a mined Case back to the
	// transcripts/ turns it came from — dropping it here is what left tune
	// with nothing to grade a production run against.
	set := proposal.Set{
		RunID:     "fp/cephblockpool/1785671212",
		SignalRef: "fp:cephblockpool",
		Status:    &proposal.Status{ObservedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
	}
	o := outcome.Outcome{
		SignalRef:  "fp:cephblockpool",
		Result:     outcome.ResultSuccess,
		ExecutedAt: time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC),
	}

	got := clank.MineCorpus([]proposal.Set{set}, []outcome.Outcome{o})

	if len(got) != 1 {
		t.Fatalf("want one case, got %d", len(got))
	}
	if diff := cmp.Diff(set.RunID, got[0].RunID); diff != "" {
		t.Error("mined case lost the joined set's RunID", diff)
	}
}

func TestMineCorpus_DropsAnOutcomeWithNoSetOpenBeforeIt(t *testing.T) {
	t.Parallel()
	// A set that opened after the outcome it's being compared against can't
	// be the one that produced it — dropping the outcome is correct, joining
	// it to a set from the future is not.
	future := proposal.Set{
		SignalRef: "fp:orphan",
		Status:    &proposal.Status{ObservedAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)},
	}
	o := outcome.Outcome{
		SignalRef:  "fp:orphan",
		ExecutedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}

	got := clank.MineCorpus([]proposal.Set{future}, []outcome.Outcome{o})

	if len(got) != 0 {
		t.Errorf("want no cases for an outcome with no eligible set, got %d", len(got))
	}
}

func TestMineCorpus_EmitsOneCasePerIncidentNotOnePerOutcomeRecord(t *testing.T) {
	t.Parallel()
	// An incident that executes and later converges publishes two outcome
	// records under the same DecisionRef — an execute-time "applied" ack, and
	// the convergence watcher's real, settled word. MineCorpus used to join a
	// Case to each of those independently, so a single incident overstated
	// itself as two rows in the corpus. Grouping by (SignalRef, DecisionRef)
	// and keeping only the terminal outcome collapses that back to one.
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	set := proposal.Set{
		SignalRef:   "fp:cephblockpool",
		Status:      &proposal.Status{ObservedAt: base},
		Proposals:   []proposal.Candidate{{ID: "c1", Confidence: 0.87}},
		Recommended: "c1",
	}
	applied := outcome.Outcome{
		SignalRef:   "fp:cephblockpool",
		DecisionRef: "dec:cephblockpool:1",
		Result:      outcome.ResultApplied,
		ExecutedAt:  base.Add(time.Hour),
	}
	settled := outcome.Outcome{
		SignalRef:   "fp:cephblockpool",
		DecisionRef: "dec:cephblockpool:1",
		Result:      outcome.ResultPartialNonConverging,
		ExecutedAt:  base.Add(2 * time.Hour),
	}

	got := clank.MineCorpus([]proposal.Set{set}, []outcome.Outcome{applied, settled})

	want := []clank.Case{{
		SignalRef:   "fp:cephblockpool",
		DecisionRef: "dec:cephblockpool:1",
		Confidence:  0.87,
		Result:      outcome.ResultPartialNonConverging,
		ObservedAt:  settled.ExecutedAt,
	}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("mined more than one case for one incident", diff)
	}
}

func TestMineCorpus_EmitsNoCaseForAnIncidentThatHasOnlyReachedApplied(t *testing.T) {
	t.Parallel()
	// "applied" is a live action's immediate word, not a settled one — the
	// convergence watcher hasn't spoken yet, so the incident is still open.
	// Case's own doc comment calls it "one closed loop"; an open incident
	// contributes none.
	set := proposal.Set{
		SignalRef:   "fp:cephblockpool",
		Status:      &proposal.Status{ObservedAt: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
		Proposals:   []proposal.Candidate{{ID: "c1", Confidence: 0.87}},
		Recommended: "c1",
	}
	applied := outcome.Outcome{
		SignalRef:   "fp:cephblockpool",
		DecisionRef: "dec:cephblockpool:1",
		Result:      outcome.ResultApplied,
		ExecutedAt:  time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC),
	}

	got := clank.MineCorpus([]proposal.Set{set}, []outcome.Outcome{applied})

	if len(got) != 0 {
		t.Errorf("want no cases for an incident still awaiting convergence, got %d", len(got))
	}
}

func TestMineCorpus_KeepsTwoCasesForTwoDifferentDecisionsOnTheSameSignal(t *testing.T) {
	t.Parallel()
	// Grouping by SignalRef alone would wrongly collapse a re-detection (a
	// second, independent decision against the same fingerprint) into the
	// first's incident. The join key has to be (SignalRef, DecisionRef).
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	set := proposal.Set{
		SignalRef:   "fp:cephblockpool",
		Status:      &proposal.Status{ObservedAt: base},
		Proposals:   []proposal.Candidate{{ID: "c1", Confidence: 0.87}},
		Recommended: "c1",
	}
	first := outcome.Outcome{
		SignalRef:   "fp:cephblockpool",
		DecisionRef: "dec:cephblockpool:1",
		Result:      outcome.ResultSuccess,
		ExecutedAt:  base.Add(time.Hour),
	}
	second := outcome.Outcome{
		SignalRef:   "fp:cephblockpool",
		DecisionRef: "dec:cephblockpool:2",
		Result:      outcome.ResultFailure,
		ExecutedAt:  base.Add(2 * time.Hour),
	}

	got := clank.MineCorpus([]proposal.Set{set}, []outcome.Outcome{first, second})

	if len(got) != 2 {
		t.Fatalf("want two cases for two distinct decisions, got %d", len(got))
	}
}
