package transcript_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/replay"
	"github.com/ianeff/thump/internal/s3test"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/transcript"
)

// fixture is a small (4-turn) real graded transcript reused as seed data —
// exercising the export against a real recorded run rather than a hand-built
// one, the same disable-cart-failure fixture internal/rca grades against.
const (
	fixtureJSONL = "../rca/testdata/graded/disable-cart-failure.jsonl"
	fixtureSet   = "../rca/testdata/graded/disable-cart-failure.set.json"
)

// TestTranscript_ProducesAPairReplayCanReproduceTheRecordedSetFrom is the
// round-trip guard: everything WalkTurns/findSet/WritePair does between a
// sealed S3 run and a replay pair must be lossless, proven against the only
// consumer that matters — replay.Propose reaches the identical Set whether
// it reads the original fixture pair or the one recovered through this
// export path.
func TestTranscript_ProducesAPairReplayCanReproduceTheRecordedSetFrom(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, bucket := s3test.New(t)
	key := testSealKey()

	turns := readFixtureTurns(t)
	runID := turns[0].RunID
	set := readFixtureSet(t)
	set.RunID = runID // stamps the join key PR4 adds — the fixture predates it

	seedTurns(ctx, t, client, key, bucket, turns)
	seedProposal(ctx, t, client, key, bucket, set)

	gotTurns, err := transcript.WalkTurns(ctx, client, key, bucket, runID)
	if err != nil {
		t.Fatalf("WalkTurns: %v", err)
	}
	if diff := cmp.Diff(turns, gotTurns); diff != "" {
		t.Error("wrong turns recovered from S3 (-want +got)", diff)
	}

	out := t.TempDir()
	jsonlPath, err := transcript.WritePair(out, gotTurns, set, true)
	if err != nil {
		t.Fatalf("WritePair: %v", err)
	}
	setPath := filepath.Join(out, "run.set.json")

	recovered, err := replay.LoadTranscript(jsonlPath, setPath)
	if err != nil {
		t.Fatalf("LoadTranscript on the recovered pair: %v", err)
	}
	original, err := replay.LoadTranscript(fixtureJSONL, fixtureSet)
	if err != nil {
		t.Fatalf("LoadTranscript on the original fixture: %v", err)
	}
	// original's Set predates the RunID field — stamp it identically so the
	// comparison below isn't defeated by the one field this test exists to add.
	original.Set.RunID = runID

	weights := clank.DefaultScoringWeights()
	wantSet, err := replay.Propose(ctx, original, weights)
	if err != nil {
		t.Fatalf("replay.Propose on the original fixture: %v", err)
	}
	gotSet, err := replay.Propose(ctx, recovered, weights)
	if err != nil {
		t.Fatalf("replay.Propose on the recovered pair: %v", err)
	}

	if diff := cmp.Diff(wantSet.Proposals, gotSet.Proposals); diff != "" {
		t.Error("replaying the exported pair did not reproduce the recorded proposals (-want +got)", diff)
	}
}

// TestTranscript_FallsBackToTheReasoningJournalWhenNeverPublished pins the
// second lookup path findSet needs: a run whose Set only ever reached
// clank/thump.reasoning/ (the gate failed, so it never published) must still
// come back found, not silently dropped because thump.proposals/ came up
// empty.
func TestTranscript_FallsBackToTheReasoningJournalWhenNeverPublished(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, bucket := s3test.New(t)
	key := testSealKey()

	turns := readFixtureTurns(t)
	runID := turns[0].RunID
	set := readFixtureSet(t)
	set.RunID = runID
	set.Status = &proposal.Status{Phase: proposal.PhaseNoAction}

	seedTurns(ctx, t, client, key, bucket, turns)
	seedJournal(ctx, t, client, key, bucket, set)

	_, found, err := transcript.FindSetForTest(ctx, client, key, bucket, runID)
	if err != nil {
		t.Fatalf("findSet: %v", err)
	}
	if !found {
		t.Error("want the journaled set found via the reasoning-journal fallback, got not-found")
	}
}

// TestTranscript_ReportsNotFoundRatherThanGuessingAtAnUnjoinedRun pins the
// refusal: a RunID with checkpointed turns but no Set anywhere (predates the
// RunID field, or the run is still in flight) must come back not-found, not
// matched to some other run's Set by proximity.
func TestTranscript_ReportsNotFoundRatherThanGuessingAtAnUnjoinedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, bucket := s3test.New(t)
	key := testSealKey()

	turns := readFixtureTurns(t)
	runID := turns[0].RunID
	seedTurns(ctx, t, client, key, bucket, turns)
	// deliberately: no proposal.Set seeded anywhere.

	_, found, err := transcript.FindSetForTest(ctx, client, key, bucket, runID)
	if err != nil {
		t.Fatalf("findSet: %v", err)
	}
	if found {
		t.Error("want not-found for a run with no joined Set, got found")
	}
}

// TestTranscript_AllExportsEveryRunAndSkipsOnesWithoutARecoverableSet pins
// bulk mode's stricter posture: unlike the single-run verb, which happily
// writes run.jsonl alone when no Set is found, --all must never mix a
// complete pair with a partial one, so a run with turns but no Set is
// skipped and reported rather than written.
func TestTranscript_AllExportsEveryRunAndSkipsOnesWithoutARecoverableSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, bucket := s3test.New(t)
	key := testSealKey()

	fixture := readFixtureTurns(t)
	fixtureSet := readFixtureSet(t)

	complete := []string{"run-a", "run-b", "run-c"}
	for i, runID := range complete {
		turns := turnsForRun(fixture, runID)
		seedTurns(ctx, t, client, key, bucket, turns)
		set := fixtureSet
		set.RunID = runID
		// each run's Set goes in its own segment object — seedProposal's fixed
		// key would let each PutObject overwrite the last run's Set.
		objKey := fmt.Sprintf("clank/thump.proposals/seg-%04d.wal", i+1)
		seedSegment(ctx, t, client, key, bucket, objKey, set)
	}

	const noSetRunID = "run-no-set"
	seedTurns(ctx, t, client, key, bucket, turnsForRun(fixture, noSetRunID))
	// deliberately: no proposal.Set seeded for noSetRunID anywhere.

	gotRunIDs, err := transcript.ListRunIDs(ctx, client, bucket)
	if err != nil {
		t.Fatalf("ListRunIDs: %v", err)
	}
	wantRunIDs := append(append([]string{}, complete...), noSetRunID)
	sort.Strings(wantRunIDs)
	if diff := cmp.Diff(wantRunIDs, gotRunIDs); diff != "" {
		t.Error("wrong runIDs discovered under transcripts/ (-want +got)", diff)
	}

	out := t.TempDir()
	written, skipped, err := transcript.WriteAll(ctx, client, key, bucket, out)
	if err != nil {
		t.Fatalf("WriteAll: %v", err)
	}

	sort.Strings(written)
	if diff := cmp.Diff(complete, written); diff != "" {
		t.Error("wrong set of runs written (-want +got)", diff)
	}
	for _, runID := range complete {
		jsonlPath := filepath.Join(out, runID, "run.jsonl")
		setPath := filepath.Join(out, runID, "run.set.json")
		tr, err := replay.LoadTranscript(jsonlPath, setPath)
		if err != nil {
			t.Fatalf("LoadTranscript(%s): %v", runID, err)
		}
		if tr.Set.RunID != runID {
			t.Errorf("wrote %s's pair with mismatched Set.RunID: want %s, got %s", runID, runID, tr.Set.RunID)
		}
	}

	wantSkipped := map[string]string{noSetRunID: "no proposal.Set found (can lag turns by up to MaxAge + ShipInterval before shipping; retry after ~11 minutes)"}
	if diff := cmp.Diff(wantSkipped, skipped); diff != "" {
		t.Error("wrong skip report (-want +got)", diff)
	}
	if _, err := os.Stat(filepath.Join(out, noSetRunID)); !os.IsNotExist(err) {
		t.Errorf("want no directory written for the skipped run %s, got err=%v", noSetRunID, err)
	}
}

// turnsForRun returns fixture's turns, each stamped with runID — a cheap
// way to seed several distinct runs from one real recorded transcript
// without hand-authoring turn content that WriteAll never inspects.
func turnsForRun(fixture []clank.Turn, runID string) []clank.Turn {
	turns := make([]clank.Turn, len(fixture))
	for i, tn := range fixture {
		tn.RunID = runID
		turns[i] = tn
	}
	return turns
}

func readFixtureTurns(t *testing.T) []clank.Turn {
	t.Helper()
	f, err := os.Open(fixtureJSONL)
	if err != nil {
		t.Fatalf("open fixture %s: %v", fixtureJSONL, err)
	}
	defer func() { _ = f.Close() }()

	var turns []clank.Turn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var t2 clank.Turn
		if err := json.Unmarshal([]byte(line), &t2); err != nil {
			t.Fatalf("decode fixture line: %v", err)
		}
		if t2.RunID == "" {
			continue // the terminalRecord {"finished": true} line
		}
		turns = append(turns, t2)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixture %s: %v", fixtureJSONL, err)
	}
	if len(turns) == 0 {
		t.Fatalf("fixture %s carries no turns", fixtureJSONL)
	}
	return turns
}

func readFixtureSet(t *testing.T) proposal.Set {
	t.Helper()
	raw, err := os.ReadFile(fixtureSet)
	if err != nil {
		t.Fatalf("open fixture %s: %v", fixtureSet, err)
	}
	var set proposal.Set
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatalf("decode fixture %s: %v", fixtureSet, err)
	}
	return set
}

func seedTurns(ctx context.Context, t *testing.T, client *s3.Client, key sealbox.Key, bucket string, turns []clank.Turn) {
	t.Helper()
	for _, tn := range turns {
		b, err := json.Marshal(tn)
		if err != nil {
			t.Fatalf("marshal turn %d: %v", tn.Step, err)
		}
		sealed, err := key.Seal(b)
		if err != nil {
			t.Fatalf("seal turn %d: %v", tn.Step, err)
		}
		objKey := "transcripts/" + tn.RunID + "/" + strconv.Itoa(tn.Step) + ".json"
		putObject(ctx, t, client, bucket, objKey, sealed)
	}
}

// seedProposal seeds set as a one-record sealed WAL segment under
// clank/thump.proposals/ — the shape corpus.Walk expects, not a bare object.
func seedProposal(ctx context.Context, t *testing.T, client *s3.Client, key sealbox.Key, bucket string, set proposal.Set) {
	t.Helper()
	seedSegment(ctx, t, client, key, bucket, "clank/thump.proposals/seg-0001.wal", set)
}

func seedJournal(ctx context.Context, t *testing.T, client *s3.Client, key sealbox.Key, bucket string, set proposal.Set) {
	t.Helper()
	seedSegment(ctx, t, client, key, bucket, "clank/thump.reasoning/seg-0001.wal", set)
}

func seedSegment(ctx context.Context, t *testing.T, client *s3.Client, key sealbox.Key, bucket, objKey string, set proposal.Set) {
	t.Helper()
	line, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal set: %v", err)
	}
	sealed, err := key.Seal(line)
	if err != nil {
		t.Fatalf("seal segment: %v", err)
	}
	putObject(ctx, t, client, bucket, objKey, sealed)
}

func putObject(ctx context.Context, t *testing.T, client *s3.Client, bucket, key string, body []byte) {
	t.Helper()
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

func testSealKey() sealbox.Key {
	var k sealbox.Key
	for i := range k {
		k[i] = byte(i)
	}
	return k
}
