package tune_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/corpus"
	"github.com/ianeff/thump/internal/s3test"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/transcript"
	"github.com/ianeff/thump/internal/tune"
)

const (
	calibrateFixtureJSONL = "../rca/testdata/graded/disable-cart-failure.jsonl"
	calibrateFixtureSet   = "../rca/testdata/graded/disable-cart-failure.set.json"
)

// TestCalibrate_ChainsExportMineAndSweepAgainstASeededBucket pins the
// phase's "done when" bar: a run sealed in the bucket, exported by
// transcript.WriteAll into its own <out>/<runID>/ pair, then mined into a
// labelled corpus, reaches tune.Main's shortfall count — proving both that
// findTranscripts now walks WriteAll's nested layout (it globbed flat
// before this phase, and found nothing) and that a mined label actually
// reaches the sweep.
func TestCalibrate_ChainsExportMineAndSweepAgainstASeededBucket(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, bucket := s3test.New(t)
	key := calibrateSealKey()

	const runID = "run-e2e"
	turns := readCalibrateFixtureTurns(t)
	for i := range turns {
		turns[i].RunID = runID
	}
	set := readCalibrateFixtureSet(t)
	set.RunID = runID
	set.SignalRef = "sig-e2e"

	seedTurns(ctx, t, client, key, bucket, turns)
	seedSegment(ctx, t, client, key, bucket, "clank/thump.proposals/seg-0001.wal", set)
	seedSegment(ctx, t, client, key, bucket, "thump/thump.outcomes/seg-0001.wal", outcome.Outcome{
		SignalRef: "sig-e2e", DecisionRef: "dec-e2e", Result: outcome.ResultSuccess,
	})

	out := t.TempDir()
	written, skipped, err := transcript.WriteAll(ctx, client, key, bucket, out)
	if err != nil {
		t.Fatalf("WriteAll: %v", err)
	}
	if diff := len(skipped); diff != 0 {
		t.Fatalf("want nothing skipped, got %v", skipped)
	}
	if len(written) != 1 || written[0] != runID {
		t.Fatalf("want %s exported, got %v", runID, written)
	}

	mined := clank.Corpus{Version: clank.CorpusVersion, Cases: clank.MineCorpus(
		[]proposal.Set{set},
		[]outcome.Outcome{{SignalRef: "sig-e2e", DecisionRef: "dec-e2e", Result: outcome.ResultSuccess}},
	)}
	labels := corpus.Labels(mined.Cases)
	if _, ok := labels[runID]; !ok {
		t.Fatalf("want %s labelled by the mine, got labels %v", runID, labels)
	}

	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	raw, err := json.Marshal(mined)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corpusPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	code := tune.Main([]string{"-transcripts", out, "-corpus", corpusPath}, &stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("want exit 0, got %d; stdout=%s", code, stdout.String())
	}
	want := "not yet: 1 labelled cases (19 short of 20) — not enough to sweep confidently\n"
	if !strings.HasSuffix(stdout.String(), want) {
		t.Errorf("want stdout to end with %q (proves the recursive walk found WriteAll's nested pair and the mined label reached tune), got %q",
			want, stdout.String())
	}
}

func readCalibrateFixtureTurns(t *testing.T) []clank.Turn {
	t.Helper()
	f, err := os.Open(calibrateFixtureJSONL)
	if err != nil {
		t.Fatalf("open fixture %s: %v", calibrateFixtureJSONL, err)
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
		var tn clank.Turn
		if err := json.Unmarshal([]byte(line), &tn); err != nil {
			t.Fatalf("decode fixture line: %v", err)
		}
		if tn.RunID == "" {
			continue // the terminalRecord {"finished": true} line
		}
		turns = append(turns, tn)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixture %s: %v", calibrateFixtureJSONL, err)
	}
	if len(turns) == 0 {
		t.Fatalf("fixture %s carries no turns", calibrateFixtureJSONL)
	}
	return turns
}

func readCalibrateFixtureSet(t *testing.T) proposal.Set {
	t.Helper()
	raw, err := os.ReadFile(calibrateFixtureSet)
	if err != nil {
		t.Fatalf("open fixture %s: %v", calibrateFixtureSet, err)
	}
	var set proposal.Set
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatalf("decode fixture %s: %v", calibrateFixtureSet, err)
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

func seedSegment[T any](ctx context.Context, t *testing.T, client *s3.Client, key sealbox.Key, bucket, objKey string, item T) {
	t.Helper()
	line, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal segment item: %v", err)
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

func calibrateSealKey() sealbox.Key {
	var k sealbox.Key
	for i := range k {
		k[i] = byte(i)
	}
	return k
}
