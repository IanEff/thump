// Package transcript is calipers transcript's verb: turn one sealed live
// run into the pair replay.LoadTranscript reads — run.jsonl (the run's
// checkpointed turns) and run.set.json (the proposal.Set it produced, when
// one is recoverable) — so a live incident becomes a replay fixture without
// a hand-rolled decrypt script.
package transcript

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/corpus"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/unseal"
)

const (
	proposalsPrefix = "clank/thump.proposals/"
	journalPrefix   = "clank/thump.reasoning/"
)

// Main exports one live run's sealed transcript and, if recoverable, its
// proposal.Set — or, with -all, every run under transcripts/ at once, each
// into its own <out>/<runID>/ pair. Returns 0 on success, 1 on any failure,
// 2 on a usage error.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("transcript", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runID := fs.String("run-id", "", "the run's fingerprint/unixnano ID, from a 'reasoned' log line's run_id field (required unless -all)")
	out := fs.String("out", "", "output directory for run.jsonl and run.set.json (required)")
	all := fs.Bool("all", false, "export every run under transcripts/, one <out>/<runID>/ pair each")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	const usage = "usage: transcript -run-id <fingerprint/unixnano> -out <dir>\n   or: transcript -all -out <dir>\n"
	if *out == "" || (*all && *runID != "") || (!*all && *runID == "") {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	ctx := context.Background()
	key, err := unseal.KeyFromEnv(os.Getenv("THUMP_SEAL_KEY"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "transcript:", err)
		return 1
	}
	cfg, err := config.LoadCorpus()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "transcript:", err)
		return 1
	}
	client, err := objectstore.NewS3Client(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "transcript:", err)
		return 1
	}

	if *all {
		return mainAll(ctx, client, key, cfg.S3Bucket, *out, stdout, stderr)
	}

	turns, err := WalkTurns(ctx, client, key, cfg.S3Bucket, *runID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "transcript:", err)
		return 1
	}
	if len(turns) == 0 {
		_, _ = fmt.Fprintf(stderr, "transcript: no checkpointed turns under transcripts/%s/\n", *runID)
		return 1
	}

	set, found, err := findSet(ctx, client, key, cfg.S3Bucket, *runID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "transcript:", err)
		return 1
	}

	jsonlPath, err := WritePair(*out, turns, set, found)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "transcript:", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "wrote %d turn(s) to %s", len(turns), jsonlPath)
	if found {
		_, _ = fmt.Fprintln(stdout, ", plus the run's proposal.Set")
	} else {
		_, _ = fmt.Fprintln(stdout, "; no proposal.Set found for this run-id (no published or journaled record)")
	}
	return 0
}

func mainAll(ctx context.Context, client *s3.Client, key sealbox.Key, bucket, out string, stdout, stderr io.Writer) int {
	written, skipped, err := WriteAll(ctx, client, key, bucket, out)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "transcript:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "wrote %d run(s) to %s\n", len(written), out)
	for _, runID := range slices.Sorted(maps.Keys(skipped)) {
		_, _ = fmt.Fprintf(stderr, "transcript: skipped %s: %s\n", runID, skipped[runID])
	}
	return 0
}

// WalkTurns recovers every checkpointed clank.Turn a live run sealed under
// transcripts/<runID>/ — one PutObject per turn (clank.S3Store.Checkpoint),
// never the multi-record WAL segments corpus.Walk reads, so each object is
// opened and decoded on its own rather than through objectstore.UnsealSegment.
// The terminal transcripts/<runID>/finish.json marker is skipped; it carries
// no Turn.
func WalkTurns(ctx context.Context, client *s3.Client, key sealbox.Key, bucket, runID string) ([]clank.Turn, error) {
	prefix := fmt.Sprintf("transcripts/%s/", runID)
	p := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})

	type stepped struct {
		step int
		turn clank.Turn
	}
	var found []stepped
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("transcript: list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			name := strings.TrimPrefix(*obj.Key, prefix)
			step, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
			if err != nil {
				continue // finish.json, or anything else this verb doesn't recognize as a turn
			}
			turn, err := getTurn(ctx, client, key, bucket, *obj.Key)
			if err != nil {
				return nil, err
			}
			found = append(found, stepped{step: step, turn: turn})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].step < found[j].step })
	turns := make([]clank.Turn, len(found))
	for i, f := range found {
		turns[i] = f.turn
	}
	return turns, nil
}

func getTurn(ctx context.Context, client *s3.Client, key sealbox.Key, bucket, objKey string) (clank.Turn, error) {
	out, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(objKey)})
	if err != nil {
		return clank.Turn{}, fmt.Errorf("transcript: get %s: %w", objKey, err)
	}
	defer func() { _ = out.Body.Close() }()
	sealed, err := io.ReadAll(out.Body)
	if err != nil {
		return clank.Turn{}, fmt.Errorf("transcript: read %s: %w", objKey, err)
	}
	raw, err := key.Open(sealed)
	if err != nil {
		return clank.Turn{}, fmt.Errorf("transcript: unseal %s: %w", objKey, err)
	}
	var t clank.Turn
	if err := json.Unmarshal(raw, &t); err != nil {
		return clank.Turn{}, fmt.Errorf("transcript: decode %s: %w", objKey, err)
	}
	return t, nil
}

// ListRunIDs returns every distinct runID with at least one object under
// transcripts/<runID>/, sorted. It lists the write-side prefix directly
// rather than the proposals journal, so a run with checkpointed turns but
// no recoverable Set is still discovered — WriteAll reports it as skipped
// rather than it never having existed.
func ListRunIDs(ctx context.Context, client *s3.Client, bucket string) ([]string, error) {
	const prefix = "transcripts/"
	p := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	found := make(map[string]struct{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("transcript: list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			rest := strings.TrimPrefix(*obj.Key, prefix)
			runID, _, ok := strings.Cut(rest, "/")
			if !ok || runID == "" {
				continue
			}
			found[runID] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(found)), nil
}

// WriteAll exports every run under transcripts/ into <outDir>/<runID>/, one
// complete run.jsonl + run.set.json pair per run. A run whose turns come
// back empty or whose proposal.Set can't be recovered is skipped and
// reported rather than half-written — bulk output must not mix complete
// pairs with partial ones the way the single-run verb tolerates, since a
// pair tune can grade needs both halves. written and skipped cover every
// runID ListRunIDs returned.
func WriteAll(ctx context.Context, client *s3.Client, key sealbox.Key, bucket, outDir string) (written []string, skipped map[string]string, err error) {
	runIDs, err := ListRunIDs(ctx, client, bucket)
	if err != nil {
		return nil, nil, err
	}
	skipped = make(map[string]string)
	for _, runID := range runIDs {
		turns, err := WalkTurns(ctx, client, key, bucket, runID)
		if err != nil {
			return nil, nil, err
		}
		if len(turns) == 0 {
			skipped[runID] = "no checkpointed turns"
			continue
		}
		set, found, err := findSet(ctx, client, key, bucket, runID)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			skipped[runID] = "no proposal.Set found"
			continue
		}
		if _, err := WritePair(filepath.Join(outDir, runID), turns, set, true); err != nil {
			return nil, nil, err
		}
		written = append(written, runID)
	}
	return written, skipped, nil
}

// findSet locates runID's proposal.Set by exact RunID match — published
// sets first (clank/thump.proposals/), falling back to the reasoning
// journal (clank/thump.reasoning/) for a run that never passed the gate. A
// run sealed before Set.RunID existed has no exact join and comes back
// not-found rather than matched by a guess.
func findSet(ctx context.Context, client *s3.Client, key sealbox.Key, bucket, runID string) (proposal.Set, bool, error) {
	for _, prefix := range []string{proposalsPrefix, journalPrefix} {
		_, lines, err := corpus.Walk(ctx, client, client, key, bucket, prefix)
		if err != nil {
			return proposal.Set{}, false, err
		}
		sets, err := corpus.DecodeEach[proposal.Set](lines)
		if err != nil {
			return proposal.Set{}, false, fmt.Errorf("transcript: decoding %s: %w", prefix, err)
		}
		for _, s := range sets {
			if s.RunID == runID {
				return s, true, nil
			}
		}
	}
	return proposal.Set{}, false, nil
}

// WritePair writes turns as run.jsonl under dir — one clank.Turn JSON line
// per turn, the shape replay.LoadTranscript scans — and, when foundSet is
// true, set as a sibling run.set.json. It returns the jsonl path written.
func WritePair(dir string, turns []clank.Turn, set proposal.Set, foundSet bool) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("transcript: mkdir %s: %w", dir, err)
	}
	jsonlPath := filepath.Join(dir, "run.jsonl")
	if err := writeTurns(jsonlPath, turns); err != nil {
		return "", err
	}
	if foundSet {
		if err := writeSet(filepath.Join(dir, "run.set.json"), set); err != nil {
			return "", err
		}
	}
	return jsonlPath, nil
}

func writeTurns(path string, turns []clank.Turn) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // G304: operator-supplied --out dir joined with a fixed filename, not user input
	if err != nil {
		return fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	for _, t := range turns {
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Errorf("transcript: marshal turn %d: %w", t.Step, err)
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("transcript: write %s: %w", path, err)
		}
	}
	return w.Flush()
}

func writeSet(path string, set proposal.Set) error {
	b, err := json.MarshalIndent(set, "", " ")
	if err != nil {
		return fmt.Errorf("transcript: marshal set: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil { //nolint:gosec // G304: operator-supplied --out dir joined with a fixed filename, not user input
		return fmt.Errorf("transcript: write %s: %w", path, err)
	}
	return nil
}
