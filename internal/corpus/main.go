package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/unseal"
)

const (
	proposalsPrefix = "clank/thump.proposals/"
	outcomesPrefix  = "thump/thump.outcomes/"
	outPath         = "internal/clank/testdata/corpus/corpus.json"
)

// Main mines every shopped proposal.Set and outcome.Outcome of S3_BUCKET,
// joins them into a clank.Corpus, and writes it to outPath.
// Returns 0 on succes, 1 on any failure.
func Main(_ []string, stdout, stderr io.Writer) int {
	ctx := context.Background()

	key, err := unseal.KeyFromEnv(os.Getenv("THUMP_SEAL_KEY"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}
	cfg, err := config.LoadCorpus()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}
	client, err := beat.NewS3Client(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}

	c, err := mine(ctx, client, key, cfg.S3Bucket)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}

	if err := writeCorpus(c); err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}
	report(stdout, c)
	return 0
}

func mine(ctx context.Context, client *s3.Client, key sealbox.Key, bucket string) (clank.Corpus, error) {
	proposalKeys, proposalLines, err := Walk(ctx, client, client, key, bucket, proposalsPrefix)
	if err != nil {
		return clank.Corpus{}, err
	}
	outcomeKeys, outcomeLines, err := Walk(ctx, client, client, key, bucket, outcomesPrefix)
	if err != nil {
		return clank.Corpus{}, err
	}

	sets, err := DecodeEach[proposal.Set](proposalLines)
	if err != nil {
		return clank.Corpus{}, fmt.Errorf("decoding %s: %w", proposalsPrefix, err)
	}
	outcomes, err := DecodeEach[outcome.Outcome](outcomeLines)
	if err != nil {
		return clank.Corpus{}, fmt.Errorf("decoding %s: %w", outcomesPrefix, err)
	}

	return clank.Corpus{
		Cases:    clank.MineCorpus(sets, outcomes),
		MinedAt:  time.Now(),
		Segments: append(proposalKeys, outcomeKeys...),
	}, nil
}

func writeCorpus(c clank.Corpus) error {
	if err := os.MkdirAll("internal/clank/testdata/corpus", 0o750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", outPath, err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

func report(w io.Writer, c clank.Corpus) {
	byClass := map[proposal.FailureClass]int{}
	for _, cs := range c.Cases {
		byClass[cs.FailureClass]++
	}
	_, _ = fmt.Fprintf(w, "mined %d cases from %d segments\n", len(c.Cases), len(c.Segments))
	for class, n := range byClass {
		_, _ = fmt.Fprintf(w, " %-24s %d\n", class, n)
	}
}
