package corpus_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/corpus"
)

// caseWithRef gives each case its own DecisionRef (equal to outcomeRef,
// since only uniqueness matters here) — mergeCorpus now collapses cases
// sharing (SignalRef, DecisionRef) to one per incident, and these tests
// exercise the union and sort behavior, not that collapse.
func caseWithRef(outcomeRef string, at time.Time) clank.Case {
	return clank.Case{OutcomeRef: outcomeRef, DecisionRef: outcomeRef, ObservedAt: at}
}

func TestMergeCorpus_KeepsPriorIncidentsWhenTheBucketNoLongerHoldsThem(t *testing.T) {
	t.Parallel()
	// The WAL bucket is per-rig and is recreated with the cluster — one mine
	// returned zero cases from an empty bucket that had held three settled
	// incidents a day earlier. A merge that truncates makes the calibration
	// record exactly as durable as the cluster it came from, so the committed
	// corpus has to be the record and the bucket only the current window.
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	existing := clank.Corpus{
		Cases:    []clank.Case{caseWithRef("out:1", base)},
		Segments: []string{"seg-1"},
	}
	mined := clank.Corpus{MinedAt: base.Add(time.Hour)}

	got := corpus.MergeCorpusForTest(existing, mined)

	want := []clank.Case{caseWithRef("out:1", base)}
	if diff := cmp.Diff(want, got.Cases); diff != "" {
		t.Error("merging an empty mine shrank the committed corpus", diff)
	}
}

func TestMergeCorpus_AddsNothingWhenTheSameBucketIsMinedTwice(t *testing.T) {
	t.Parallel()
	// Merging on OutcomeRef, not on position: a second mine of an unchanged
	// bucket is a no-op — the same "redelivery is boring" property I-14
	// already demands of every consumer on the wire.
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	mined := clank.Corpus{
		Cases:    []clank.Case{caseWithRef("out:1", base), caseWithRef("out:2", base.Add(time.Hour))},
		Segments: []string{"seg-1", "seg-2"},
		MinedAt:  base.Add(2 * time.Hour),
	}

	first := corpus.MergeCorpusForTest(clank.Corpus{}, mined)
	second := corpus.MergeCorpusForTest(first, mined)

	if diff := cmp.Diff(first, second); diff != "" {
		t.Error("mining the same bucket twice changed the corpus", diff)
	}
}

func TestMergeCorpus_UnionsNewCasesWithoutDroppingOldOnes(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	existing := clank.Corpus{Cases: []clank.Case{caseWithRef("out:1", base)}}
	mined := clank.Corpus{
		Cases:   []clank.Case{caseWithRef("out:2", base.Add(time.Hour))},
		MinedAt: base.Add(time.Hour),
	}

	got := corpus.MergeCorpusForTest(existing, mined)

	want := []clank.Case{caseWithRef("out:1", base), caseWithRef("out:2", base.Add(time.Hour))}
	if diff := cmp.Diff(want, got.Cases); diff != "" {
		t.Error("union of existing and mined cases is wrong", diff)
	}
}

func TestMergeCorpus_SortsTheUnionByObservedAtRegardlessOfInputOrder(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	existing := clank.Corpus{Cases: []clank.Case{caseWithRef("out:later", base.Add(time.Hour))}}
	mined := clank.Corpus{Cases: []clank.Case{caseWithRef("out:earlier", base)}}

	got := corpus.MergeCorpusForTest(existing, mined)

	want := []clank.Case{caseWithRef("out:earlier", base), caseWithRef("out:later", base.Add(time.Hour))}
	if diff := cmp.Diff(want, got.Cases); diff != "" {
		t.Error("merged cases are not sorted by ObservedAt", diff)
	}
}

func TestWriteCorpus_CreatesTheFileWhenNoneExistsYet(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "corpus.json")
	mined := clank.Corpus{Cases: []clank.Case{caseWithRef("out:1", time.Now())}}

	if err := corpus.WriteCorpusForTest(path, mined); err != nil {
		t.Fatal(err)
	}

	got, err := readCorpusForTest(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(mined.Cases, got.Cases); diff != "" {
		t.Error("first-ever write did not persist the mined cases", diff)
	}
}

func TestWriteCorpus_MergesIntoWhateverIsAlreadyOnDisk(t *testing.T) {
	t.Parallel()
	// The whole point of the merge: a second writeCorpus call against the
	// same path must grow the file, not replace it.
	path := filepath.Join(t.TempDir(), "corpus.json")
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	if err := corpus.WriteCorpusForTest(path, clank.Corpus{Cases: []clank.Case{caseWithRef("out:1", base)}}); err != nil {
		t.Fatal(err)
	}
	if err := corpus.WriteCorpusForTest(path, clank.Corpus{Cases: []clank.Case{caseWithRef("out:2", base.Add(time.Hour))}}); err != nil {
		t.Fatal(err)
	}

	got, err := readCorpusForTest(t, path)
	if err != nil {
		t.Fatal(err)
	}
	want := []clank.Case{caseWithRef("out:1", base), caseWithRef("out:2", base.Add(time.Hour))}
	if diff := cmp.Diff(want, got.Cases); diff != "" {
		t.Error("second write did not merge with the first", diff)
	}
}

func readCorpusForTest(t *testing.T, path string) (clank.Corpus, error) {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: t.TempDir path constructed in-test, not user input
	if err != nil {
		return clank.Corpus{}, err
	}
	var c clank.Corpus
	if err := json.Unmarshal(b, &c); err != nil {
		return clank.Corpus{}, err
	}
	return c, nil
}
