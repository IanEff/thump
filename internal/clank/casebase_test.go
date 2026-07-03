package clank_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/outcome"
)

func TestCaseBase_RefusesAnUnprovenancedCase(t *testing.T) {
	t.Parallel()
	cases := map[string]func(c *clank.Case){
		"Append refuses a case with no fingerprint":  func(c *clank.Case) { c.Fingerprint = "" },
		"Append refuses a case with no outcome ref":  func(c *clank.Case) { c.OutcomeRef = "" },
		"Append refuses a case with no decision ref": func(c *clank.Case) { c.DecisionRef = "" },
		"Append refuses a case with no result":       func(c *clank.Case) { c.Result = "" },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := goldenCase()
			breakIt(&c)

			err := clank.NewCaseBase().Append(c)

			if !errors.Is(err, clank.ErrUnprovenancedCase) {
				t.Errorf("a case that can't be traced is poison, got %v", err)
			}
		})
	}
}

func TestCaseBase_WhatClickLearnedIsQueryable(t *testing.T) {
	t.Parallel()
	cb := clank.NewCaseBase()

	first := goldenCase()
	second := goldenCase()
	second.OutcomeRef, second.Result = "out:slo_burn:ceph-rgw:2000", outcome.ResultFailure
	other := goldenCase()
	other.Fingerprint, other.OutcomeRef = "slo_burn:somebody-else", "out:slo_burn:somebody-else:1000"

	for _, c := range []clank.Case{first, second, other} {
		if err := cb.Append(c); err != nil {
			t.Fatal("a provenanced case must append:", err)
		}
	}

	// the audit query: everything we ever learned about one fingerprint,
	// in the order we learned it
	if diff := cmp.Diff([]clank.Case{first, second}, cb.Cases("slo_burn:ceph-rgw")); diff != "" {
		t.Error("the case base answered wrong for its fingerprint (-want +got)", diff)
	}
	if diff := cmp.Diff([]clank.Case(nil), cb.Cases("slo_burn:never-seen")); diff != "" {
		t.Error("an unknown fingerprint has no history, not an invented one (-want +got)", diff)
	}
}

func TestCaseBase_AlignmentNeedsCorroboration(t *testing.T) {
	t.Parallel()
	cb := clank.NewCaseBase()

	// zero cases: no history, no opinion.
	if rate, ok := cb.Alignment("slo_burn:ceph-rgw"); ok {
		t.Errorf("an empty case base must not corroborate anything, got rate %v", rate)
	}

	// ONE live success: an anecdote, not evidence (minCorroboration = 2 —
	// I-6 defence 1's ≥2-source floor, applied at the learning edge).
	mustAppend(t, cb, liveCase(outcome.ResultSuccess, "out:1"))
	if rate, ok := cb.Alignment("slo_burn:ceph-rgw"); ok {
		t.Errorf("a single live outcome is an anecdote and must not corroborate, got rate %v", rate)
	}

	// TWO live successes: reality has voted twice; now there's a rate.
	mustAppend(t, cb, liveCase(outcome.ResultSuccess, "out:2"))
	rate, ok := cb.Alignment("slo_burn:ceph-rgw")
	if !ok {
		t.Fatal("two corroborating live outcomes must produce an alignment")
	}
	if diff := cmp.Diff(1.0, rate); diff != "" {
		t.Error("two successes out of two is a rate of 1.0 (-want +got)", diff)
	}
}

func TestCaseBase_RehearsalsNeverMoveAlignment(t *testing.T) {
	t.Parallel()
	cb := clank.NewCaseBase()

	for i := range 5 {
		mustAppend(t, cb, rehearsalCase(fmt.Sprintf("out:rehearsal-%d", i)))
	}

	if rate, ok := cb.Alignment("slo_burn:ceph-rgw"); ok {
		t.Errorf("a rehearsal is not evidence — dry-run cases must never corroborate (rate %v). "+
			"Learning from rehearsals is silent-learning-corruption by construction.", rate)
	}
	// …but the bookkeeping happened: all five are in the audit trail.
	if got := len(cb.Cases("slo_burn:ceph-rgw")); got != 5 {
		t.Errorf("bookkeeping, not belief — want all 5 rehearsals banked, got %d", got)
	}
}

func TestCaseBase_AlignmentIsTheObservedSuccessRate(t *testing.T) {
	t.Parallel()
	cb := clank.NewCaseBase()

	// 2 successes, 1 failure, 1 partial: rate = 2/4. partial_non_converging
	// counts in the denominator — "it half-worked and isn't settling" is
	// evidence AGAINST the action, which is the entire reason I-6 defence 4
	// demanded the value be representable from birth. This claim is where
	// that four-wave-old promise gets cashed.
	mustAppend(t, cb, liveCase(outcome.ResultSuccess, "out:1"))
	mustAppend(t, cb, liveCase(outcome.ResultSuccess, "out:2"))
	mustAppend(t, cb, liveCase(outcome.ResultFailure, "out:3"))
	mustAppend(t, cb, liveCase(outcome.ResultPartialNonConverging, "out:4"))

	rate, ok := cb.Alignment("slo_burn:ceph-rgw")
	if !ok {
		t.Fatal("four live terminal outcomes are corroborated history")
	}
	if diff := cmp.Diff(0.5, rate); diff != "" {
		t.Error("2 clean successes out of 4 live terminal outcomes is 0.5 (-want +got)", diff)
	}

	// a live UNKNOWN carries no information in either direction — it joins
	// the audit trail and stays out of the arithmetic entirely.
	mustAppend(t, cb, liveCase(outcome.ResultUnknown, "out:5"))
	rate, _ = cb.Alignment("slo_burn:ceph-rgw")
	if diff := cmp.Diff(0.5, rate); diff != "" {
		t.Error("an unknown outcome must not move the rate either way (-want +got)", diff)
	}
}

func goldenCase() clank.Case {
	return clank.Case{
		Fingerprint:  "slo_burn:ceph-rgw",             // Outcome.SignalRef — five beats, one string
		DecisionRef:  "dec:slo_burn:ceph-rgw:1000",    // Outcome.DecisionRef — the grant
		OutcomeRef:   "out:slo_burn:ceph-rgw:1000",    // Outcome.ID — the leaf this case summarizes
		ContractRef:  "throttle-non-critical-paths",   // Outcome.ContractRef
		FailureClass: clank.ClassDependencySaturation, // set.FailureClass — for smarter retrieval, someday
		Confidence:   0.87,                            // set's recommended candidate — CE's stated half
		Mode:         outcome.ModeLive,
		Result:       outcome.ResultSuccess,
		ObservedAt:   time.Unix(1000, 0), // Outcome.ExecutedAt — click has no clock
	}
}

func liveCase(r outcome.Result, ref string) clank.Case {
	c := goldenCase()
	c.Result, c.OutcomeRef = r, ref
	return c
}

func rehearsalCase(ref string) clank.Case {
	c := goldenCase()
	c.Mode, c.Result, c.OutcomeRef = outcome.ModeDryRun, outcome.ResultRendered, ref
	return c
}

func mustAppend(t *testing.T, cb *clank.CaseBase, c clank.Case) {
	t.Helper()
	if err := cb.Append(c); err != nil {
		t.Fatal("fixture case must append:", err)
	}
}
