package tune_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/grade"
	"github.com/ianeff/thump/internal/rca"
	"github.com/ianeff/thump/internal/tune"
)

// gradedTranscriptPaths globs the committed graded corpus into the
// TranscriptPaths pairs Run expects — every .jsonl beside its .set.json,
// checked into internal/rca/testdata/graded/ so the sweep runs with no
// ANTHROPIC_API_KEY and no rig.
func gradedTranscriptPaths(t *testing.T) []tune.TranscriptPaths {
	t.Helper()

	matches, err := filepath.Glob("../rca/testdata/graded/*.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	paths := make([]tune.TranscriptPaths, len(matches))
	for i, jsonl := range matches {
		stem := strings.TrimSuffix(jsonl, ".jsonl")
		paths[i] = tune.TranscriptPaths{JSONL: jsonl, Set: stem + ".set.json"}
	}
	return paths
}

// TestRun_DropsATranscriptThatFailsToReplayAndStillReturnsGridPoints pins the
// fix for truncated.jsonl aborting the whole sweep: replay's own truncation
// fixture is deliberately unplayable, and one bad transcript must not stop
// every other transcript in the corpus from being swept.
func TestRun_DropsATranscriptThatFailsToReplayAndStillReturnsGridPoints(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{Transcripts: []tune.TranscriptPaths{
		{JSONL: "../replay/testdata/slo_burn-cart.jsonl", Set: "../replay/testdata/slo_burn-cart.set.json"},
		{JSONL: "../replay/testdata/truncated.jsonl", Set: "../replay/testdata/truncated.set.json"},
	}}

	points, err := tune.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) == 0 {
		t.Error("want grid points from the surviving transcript, got none")
	}
}

// TestRun_ErrorsWhenEveryTranscriptFailsToReplay pins the other side: a
// sweep whose entire corpus is unplayable must fail loudly rather than
// silently reporting an empty, meaningless grid.
func TestRun_ErrorsWhenEveryTranscriptFailsToReplay(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{Transcripts: []tune.TranscriptPaths{
		{JSONL: "../replay/testdata/truncated.jsonl", Set: "../replay/testdata/truncated.set.json"},
	}}

	if _, err := tune.Run(context.Background(), cfg); err == nil {
		t.Error("want an error when no transcript in the corpus can replay, got nil")
	}
}

// TestRun_ScoresGroundedRowsThatVaryAcrossTheGrid is the phase's central
// claim: the sweep reads labels, not just numbers. A grid whose Grounded
// count is identical at every point is measuring a flat surface, which is
// exactly what maximizing MeanConfidence did for three phases.
func TestRun_ScoresGroundedRowsThatVaryAcrossTheGrid(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{
		Transcripts: gradedTranscriptPaths(t),
		Cases:       rca.Table(),
		Objective:   tune.DefaultObjective(),
	}

	points, err := tune.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[int]bool{}
	for _, p := range points {
		seen[p.Grounded] = true
	}
	if len(seen) < 2 {
		t.Errorf("want Grounded to vary across the grid, got the same count %v at every point", seen)
	}
}

// TestRun_ErrorsWhenNoTranscriptPairsToAGradedCase pins the fail-closed
// rule. A Grounded of 0 reads identically to "every row failed", so a sweep
// that silently graded nothing would report a real-looking zero surface and
// a NotYet that sounds like a finding. It must refuse instead.
func TestRun_ErrorsWhenNoTranscriptPairsToAGradedCase(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{
		Transcripts: []tune.TranscriptPaths{{
			JSONL: "../replay/testdata/slo_burn-cart.jsonl",
			Set:   "../replay/testdata/slo_burn-cart.set.json",
		}},
		Cases:     []rca.Case{{Name: "a case that pairs to no transcript", Fixture: "nonexistent.yaml"}},
		Objective: tune.DefaultObjective(),
	}

	if _, err := tune.Run(context.Background(), cfg); err == nil {
		t.Error("want an error when no transcript pairs to a graded case, got nil")
	}
}

// runIDLabelledTranscripts pairs two graded fixtures whose real RunID (read
// from the checked-in transcript, not the filename) names them, with no
// rca.Case naming either — a production-shaped pull where the only way to
// grade them is by the RunID a settled Label carries.
func runIDLabelledTranscripts() []tune.TranscriptPaths {
	return []tune.TranscriptPaths{
		{JSONL: "../rca/testdata/graded/node-death.jsonl", Set: "../rca/testdata/graded/node-death.set.json"},
		{JSONL: "../rca/testdata/graded/ceph-osd-latency.jsonl", Set: "../rca/testdata/graded/ceph-osd-latency.set.json"},
	}
}

// TestRun_GradesATranscriptByItsRunIDLabelWhenNoCaseMatchesItsStem pins the
// phase's hinge: a run pulled from a cluster nobody here has seen is named
// after its RunID, not a fixture stem, so it can only be graded if Labels is
// consulted at all. Cases is empty, so this Grounded count could only have
// come from the label path. A label's Correct verdict is a settled fact, not
// a function of the swept weights, so Grounded from labelled rows holds
// steady at exactly the one Correct label across every grid point — unlike a
// rubric row, whose Pass can flip with the weights being swept.
func TestRun_GradesATranscriptByItsRunIDLabelWhenNoCaseMatchesItsStem(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{
		Transcripts: runIDLabelledTranscripts(),
		Labels: map[string]grade.Label{
			"slo_burn:ceph-cluster/1786363080779796000": {RunID: "slo_burn:ceph-cluster/1786363080779796000", Correct: true, Source: grade.SourceConverged},
			"slo_burn:ceph-osd/1786363093747503000":     {RunID: "slo_burn:ceph-osd/1786363093747503000", Correct: false, Source: grade.SourceReversed},
		},
		Objective: tune.DefaultObjective(),
	}

	points, err := tune.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) == 0 {
		t.Fatal("want grid points from the labelled transcripts, got none")
	}

	for _, p := range points {
		if p.Grounded != 1 {
			t.Errorf("want the one Correct label grounded at every grid point, got Grounded=%d at %+v", p.Grounded, p)
		}
	}
}

// TestRun_LeavesATranscriptUngradedWhenNeitherALabelNorACaseMatchesIt pins
// the other side: with no Cases and no Labels naming either transcript, the
// objective must not invent a verdict — every grid point's Grounded count
// stays zero rather than the pre-flight check refusing to run at all, since
// a sweep can still report its confidence-only numbers for an ungraded
// corpus.
func TestRun_LeavesATranscriptUngradedWhenNeitherALabelNorACaseMatchesIt(t *testing.T) {
	t.Parallel()

	cfg := tune.SweepConfig{
		Transcripts: runIDLabelledTranscripts(),
		Objective:   tune.DefaultObjective(),
	}

	points, err := tune.Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range points {
		if p.Grounded != 0 {
			t.Errorf("want every point ungraded (Grounded == 0) with no label or case to judge these transcripts, got %+v", p)
		}
	}
}
