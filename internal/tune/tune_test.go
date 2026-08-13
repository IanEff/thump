package tune_test

import (
	"io"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/tune"
)

// TestTune_LeavesEveryConfigFileByteIdentical pins the authority boundary. The
// tuning surface reads and proposes; the authority to write the governance
// floors hiss enforces has never been granted, and a flag added quietly is how
// it would get granted without anyone deciding to.
func TestTune_LeavesEveryConfigFileByteIdentical(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ file string }{
		"tune leaves config/clank/weights.yaml untouched":                     {file: "../../config/clank/weights.yaml"},
		"tune leaves dev config/dev/hiss/policy.yaml untouched":               {file: "../../config/dev/hiss/policy.yaml"},
		"tune leaves thump-test config/thump-test/hiss/policy.yaml untouched": {file: "../../config/thump-test/hiss/policy.yaml"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			before, err := os.ReadFile(tc.file) //nolint:gosec // G304: fixed config path, not user input
			if err != nil {
				t.Fatal(err)
			}
			if code := tune.Main([]string{"--json"}, io.Discard, io.Discard); code > 1 {
				t.Fatalf("sweep exited %d", code)
			}
			after, err := os.ReadFile(tc.file) //nolint:gosec // G304: fixed config path, not user input
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(string(before), string(after)); diff != "" {
				t.Error("tune mutated a config file it must not touch", diff)
			}
		})
	}
}

// TestMain_RejectsAnApplyFlagRatherThanIgnoringIt keeps the refusal loud. An
// unknown flag that exits 2 is a refusal a human reads; a flag silently ignored
// is one somebody assumes worked.
func TestMain_RejectsAnApplyFlagRatherThanIgnoringIt(t *testing.T) {
	t.Parallel()

	if got := tune.Main([]string{"--apply"}, io.Discard, io.Discard); got != 2 {
		t.Error("want exit 2 for --apply", got)
	}
}

// TestDecide_BracketsGroundingOneBetweenTheFloorAndTheCeiling is the payoff.
// Grounded is non-zero only in a contiguous band because the labels bound it
// from below (Confidence < WantConfidenceAtLeast) and from above
// (ConfidenceCeilingBound flips true) — so a band is a recommendation, where
// a maximum never was.
func TestDecide_BracketsGroundingOneBetweenTheFloorAndTheCeiling(t *testing.T) {
	t.Parallel()

	points := []tune.Point{
		{GroundingOne: 0.4, Grounded: 0}, // below every row's floor
		{GroundingOne: 0.5, Grounded: 3}, // inside
		{GroundingOne: 0.6, Grounded: 3}, // inside
		{GroundingOne: 0.7, Grounded: 0}, // ceiling binds
	}

	got, notYet := tune.DecideForTest(points)
	if notYet.Reason != "" {
		t.Fatalf("want a Proposal from a bracketed grid, got NotYet: %s", notYet.Reason)
	}

	want := tune.Proposal{File: "config/clank/weights.yaml", Key: "groundingOne", To: 0.55}
	if diff := cmp.Diff(want.Key, got.Key); diff != "" {
		t.Error("wrong tuned key", diff)
	}
	if diff := cmp.Diff(want.To, got.To); diff != "" {
		t.Error("wrong bracket midpoint", diff)
	}
	if got.Basis == "" {
		t.Error("want a Basis naming the rows that bound the bracket, got an empty string")
	}
}

// TestDecide_StaysNotYetWhenEveryGridPointScoresAlike keeps the honest close
// available. A flat Grounded surface means the labels cannot distinguish
// these weights, which is a closed track and not a recommendation.
func TestDecide_StaysNotYetWhenEveryGridPointScoresAlike(t *testing.T) {
	t.Parallel()

	points := []tune.Point{
		{GroundingOne: 0.4, Grounded: 3},
		{GroundingOne: 0.5, Grounded: 3},
		{GroundingOne: 0.6, Grounded: 3},
	}

	_, notYet := tune.DecideForTest(points)
	if notYet.Reason == "" {
		t.Error("want NotYet from a flat graded surface, got a Proposal")
	}
}

// TestDecide_NeverBlendsGroundedIntoSupport pins objective.go's own refusal:
// an RCA score and a floor's admitted-wins are different questions, and one
// blended number is the gate-vs-shaper mistake applied to tuning. decide
// takes no Corpus, so Support can only ever ride along empty — never
// fabricated from Grounded.
func TestDecide_NeverBlendsGroundedIntoSupport(t *testing.T) {
	t.Parallel()

	points := []tune.Point{
		{GroundingOne: 0.4, Grounded: 0},
		{GroundingOne: 0.5, Grounded: 3},
		{GroundingOne: 0.6, Grounded: 3},
		{GroundingOne: 0.7, Grounded: 0},
	}

	got, notYet := tune.DecideForTest(points)
	if notYet.Reason != "" {
		t.Fatalf("want a Proposal from a bracketed grid, got NotYet: %s", notYet.Reason)
	}
	if len(got.Support) != 0 {
		t.Error("want Support empty — decide has no Corpus to read and must not fabricate one from Grounded", got.Support)
	}
}
