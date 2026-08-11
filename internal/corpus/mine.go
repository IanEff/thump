package corpus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/unseal"
)

const (
	proposalsPrefix = "clank/thump.proposals/"
	outcomesPrefix  = "thump/thump.outcomes/"
	outPath         = "internal/clank/testdata/corpus/corpus.json"
)

// ErrUnknownCorpusVersion means the artifact on disk was written by a
// newer build than this one.
var ErrUnknownCorpusVersion = errors.New("corpus: artifact version is newer than this build understands")

// Main mines every shipped proposal.Set and outcome.Outcome of S3_BUCKET,
// joins them into a clank.Corpus, and writes it to outPath.
// Returns 0 on success, 1 on any failure.
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
	client, err := objectstore.NewS3Client(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3TLSInsecureSkipVerify)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}

	c, err := mine(ctx, client, key, cfg.S3Bucket)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}

	if err := writeCorpus(outPath, c); err != nil {
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

func writeCorpus(path string, mined clank.Corpus) error {
	existing, err := readCorpus(path)
	if err != nil {
		return err
	}
	merged := mergeCorpus(existing, mined)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // nolint:gosec // G304: operator-authored fixed path (outPath const), not user input
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(merged); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

// mergeCorpus unions existing and mined on OutcomeRef, collapses the union
// to one Case per incident (existing may predate that invariant, or mined
// may re-observe an incident existing already settled), sorts by
// ObservedAt, and takes mined's MinedAt as the new "latest mined" timestamp.
func mergeCorpus(existing, mined clank.Corpus) clank.Corpus {
	seenCase := make(map[string]bool, len(existing.Cases)+len(mined.Cases))
	cases := make([]clank.Case, 0, len(existing.Cases)+len(mined.Cases))

	for _, c := range slices.Concat(existing.Cases, mined.Cases) {
		if seenCase[c.OutcomeRef] {
			continue
		}
		seenCase[c.OutcomeRef] = true
		cases = append(cases, c)
	}
	cases = clank.CollapseCases(cases)

	slices.SortFunc(cases, func(a, b clank.Case) int { return a.ObservedAt.Compare(b.ObservedAt) })

	seenSeg := make(map[string]bool, len(existing.Segments)+len(mined.Segments))
	segments := make([]string, 0, len(existing.Segments)+len(mined.Segments))
	for _, s := range slices.Concat(existing.Segments, mined.Segments) {
		if seenSeg[s] {
			continue
		}
		seenSeg[s] = true
		segments = append(segments, s)
	}
	slices.Sort(segments)

	return clank.Corpus{Version: clank.CorpusVersion, Cases: cases, MinedAt: mined.MinedAt, Segments: segments}
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

// legacyCorpus is the pre-tag layout: exported Go field names as JSON keys,
// and Fingerprint where Case now says signalRef.
type legacyCorpus struct {
	Cases []struct {
		Fingerprint  string                `json:"Fingerprint"`
		SignalRef    string                `json:"SignalRef"`
		DecisionRef  string                `json:"DecisionRef"`
		OutcomeRef   string                `json:"OutcomeRef"`
		ContractRef  string                `json:"ContractRef"`
		FailureClass proposal.FailureClass `json:"FailureClass"`
		Confidence   float64               `json:"Confidence"`
		Mode         outcome.Mode          `json:"Mode"`
		Result       outcome.Result        `json:"Result"`
		ObservedAt   time.Time             `json:"ObservedAt"`
	} `json:"Cases"`
	MinedAt  time.Time `json:"MinedAt"`
	Segments []string  `json:"Segments"`
}

// signalRefFromDecisionRef recovers the fingerprint a pre-tag artifact
// lost: a DecisionRef is "dec:" + fingerprint + ":" + unix seconds, and the
// fingerprint itself can contain colons ("slo_burn:ceph-cluster"), so the
// split is on the first and last separators, never on every one.
func signalRefFromDecisionRef(ref string) string {
	rest, ok := strings.CutPrefix(ref, "dec:")
	if !ok {
		return ""
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return ""
	}
	return rest[:i]
}

// migrateLegacy decodes a pre-tag artifact and recovers each case's
// SignalRef from whichever source still has it — the field itself when the
// artifact predates the rename, DecisionRef when the rename already
// emptied it.
func migrateLegacy(raw []byte) (clank.Corpus, error) {
	var legacy legacyCorpus
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return clank.Corpus{}, fmt.Errorf("decode legacy corpus: %w", err)
	}

	cases := make([]clank.Case, 0, len(legacy.Cases))
	for _, lc := range legacy.Cases {
		signalRef := lc.SignalRef
		if signalRef == "" {
			signalRef = lc.Fingerprint
		}
		if signalRef == "" {
			signalRef = signalRefFromDecisionRef(lc.DecisionRef)
		}
		cases = append(cases, clank.Case{
			SignalRef:    signalRef,
			DecisionRef:  lc.DecisionRef,
			OutcomeRef:   lc.OutcomeRef,
			ContractRef:  lc.ContractRef,
			FailureClass: lc.FailureClass,
			Confidence:   lc.Confidence,
			Mode:         lc.Mode,
			Result:       lc.Result,
			ObservedAt:   lc.ObservedAt,
		})
	}

	return clank.Corpus{
		Version:  clank.CorpusVersion,
		Cases:    cases,
		MinedAt:  legacy.MinedAt,
		Segments: legacy.Segments,
	}, nil
}

func readCorpus(path string) (clank.Corpus, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-authored fixed path (outPath const), not user input
	if errors.Is(err, os.ErrNotExist) {
		return clank.Corpus{Version: clank.CorpusVersion}, nil
	}
	if err != nil {
		return clank.Corpus{}, fmt.Errorf("open %s: %w", path, err)
	}

	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return clank.Corpus{}, fmt.Errorf("decode %s: %w", path, err)
	}
	switch {
	case probe.Version == 0:
		return migrateLegacy(raw)
	case probe.Version > clank.CorpusVersion:
		return clank.Corpus{}, fmt.Errorf("%w: %s is version %d, this build writes %d", ErrUnknownCorpusVersion, path, probe.Version, clank.CorpusVersion)
	}

	var c clank.Corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		return clank.Corpus{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return c, nil
}
