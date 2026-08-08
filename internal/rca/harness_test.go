package rca_test

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/rca"
)

// TestTranscriptName_MatchesTheStemWriteSetActuallyProduces pins the pairing
// rule to a single definition. tune globs a directory rca wrote and maps each
// file back to the Case holding its answers; if the two packages each spell
// the fixture-to-stem rule themselves, a sweep grades transcripts against the
// wrong rows and reports a confident wrong bracket.
func TestTranscriptName_MatchesTheStemWriteSetActuallyProduces(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := rca.Table()[0]

	// WriteSetForTest is the export_test.go forwarder for the unexported
	// writeSet — the one seam, per AGENTS.md §1.
	if err := rca.WriteSetForTest(dir, c.Fixture, proposal.Set{}); err != nil {
		t.Fatal(err)
	}

	want := rca.TranscriptName(c) + ".set.json"
	got, err := filepath.Glob(filepath.Join(dir, "*.set.json"))
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{want}, []string{filepath.Base(got[0])}); diff != "" {
		t.Error("TranscriptName does not name the file writeSet actually wrote", diff)
	}
}

// TestTranscriptName_IsOneToOneAcrossTheGradedSuite pins the property the
// SignalRef-keyed rule lacked: eight cases, eight distinct stems. Under the
// old rule six stems covered eight cases, so two transcripts were overwritten
// on every run and the suite stayed green while losing a quarter of its data.
func TestTranscriptName_IsOneToOneAcrossTheGradedSuite(t *testing.T) {
	t.Parallel()

	table := rca.Table()
	stems := make(map[string]string, len(table))
	for _, c := range table {
		if prior, clash := stems[rca.TranscriptName(c)]; clash {
			t.Errorf("two cases share one transcript stem — %q and %q both write %q",
				prior, c.Name, rca.TranscriptName(c))
		}
		stems[rca.TranscriptName(c)] = c.Name
	}

	if diff := cmp.Diff(len(table), len(stems)); diff != "" {
		t.Error("wrong number of distinct transcript stems for the graded suite", diff)
	}
}
