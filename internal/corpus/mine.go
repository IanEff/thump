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
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/grade"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/sealbox"
	"github.com/ianeff/thump/internal/unseal"
)

const (
	proposalsPrefix = "clank/thump.proposals/"
	outcomesPrefix  = "thump/thump.outcomes/"
	// journalPrefix holds every terminal-phase proposal.Set clank journals,
	// gated or not — a superset of proposalsPrefix, never joined into Cases
	// here, only counted, since turning a miss into a graded row needs a
	// replay pair (calipers transcript), not a bare journal line.
	journalPrefix = "clank/thump.reasoning/"
	outPath       = "internal/clank/testdata/corpus/corpus.json"
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
	client, err := objectstore.NewS3Client(ctx, cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}

	c, pop, err := mine(ctx, client, key, cfg.S3Bucket)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}

	if err := writeCorpus(outPath, c); err != nil {
		_, _ = fmt.Fprintln(stderr, "corpus:", err)
		return 1
	}
	report(stdout, c, pop)
	return 0
}

// populations partitions what mine() saw into what tune can act on, so the
// corpus reports its own usable size instead of implying every row is
// gradeable.
type populations struct {
	Journaled  int // every terminal-phase proposal.Set observed, gated or not
	Labelled   int // settled cases — grade.FromRecord returned ok
	InFlight   int // every outcome record seen for the incident is still ResultApplied
	Unlabelled int // journaled as declined; no outcome will ever exist to label it
}

func mine(ctx context.Context, client *s3.Client, key sealbox.Key, bucket string) (clank.Corpus, populations, error) {
	proposalKeys, proposalLines, err := Walk(ctx, client, client, key, bucket, proposalsPrefix)
	if err != nil {
		return clank.Corpus{}, populations{}, err
	}
	outcomeKeys, outcomeLines, err := Walk(ctx, client, client, key, bucket, outcomesPrefix)
	if err != nil {
		return clank.Corpus{}, populations{}, err
	}
	journalKeys, journalLines, err := Walk(ctx, client, client, key, bucket, journalPrefix)
	if err != nil {
		return clank.Corpus{}, populations{}, err
	}

	sets, err := DecodeEach[proposal.Set](proposalLines)
	if err != nil {
		return clank.Corpus{}, populations{}, fmt.Errorf("decoding %s: %w", proposalsPrefix, err)
	}
	outcomes, err := DecodeEach[outcome.Outcome](outcomeLines)
	if err != nil {
		return clank.Corpus{}, populations{}, fmt.Errorf("decoding %s: %w", outcomesPrefix, err)
	}
	journaled, err := DecodeEach[proposal.Set](journalLines)
	if err != nil {
		return clank.Corpus{}, populations{}, fmt.Errorf("decoding %s: %w", journalPrefix, err)
	}

	cases := clank.MineCorpus(sets, outcomes)

	pop := populations{
		Journaled:  len(journaled),
		Labelled:   labelledCount(cases),
		InFlight:   inFlightCount(outcomes),
		Unlabelled: declinedCount(journaled),
	}

	return clank.Corpus{
		Cases:    cases,
		MinedAt:  time.Now(),
		Segments: slices.Concat(proposalKeys, outcomeKeys, journalKeys),
	}, pop, nil
}

// labelledCount routes through grade.FromRecord rather than reimplementing
// its rule — every case MineCorpus returns is already settled and collapsed,
// so this is a count today, but stays correct if FromRecord's rule changes.
func labelledCount(cases []clank.Case) int {
	n := 0
	for _, cs := range cases {
		set := proposal.Set{RunID: cs.RunID}
		out := outcome.Outcome{Result: cs.Result}
		if _, ok := grade.FromRecord(set, decision.Decision{}, out); ok {
			n++
		}
	}
	return n
}

// inFlightCount reports how many (SignalRef, DecisionRef) incidents have
// only ever produced an execute-time ResultApplied record — exactly the
// population clank.CollapseCases drops, since nothing has settled them yet.
func inFlightCount(outcomes []outcome.Outcome) int {
	type key struct{ signalRef, decisionRef string }
	settled := make(map[key]bool)
	seen := make(map[key]bool)
	for _, o := range outcomes {
		k := key{o.SignalRef, o.DecisionRef}
		seen[k] = true
		if o.Result != outcome.ResultApplied {
			settled[k] = true
		}
	}
	n := 0
	for k := range seen {
		if !settled[k] {
			n++
		}
	}
	return n
}

// declinedCount reports how many journaled sets governance ruled against
// before thump ever rendered them — a declined Set never produces an
// Outcome (proposal.go:126), so nothing but the journal itself ever
// distinguishes this population from a run still in flight.
func declinedCount(journaled []proposal.Set) int {
	n := 0
	for _, s := range journaled {
		if s.Status != nil && s.Status.Phase == proposal.PhaseDeclined {
			n++
		}
	}
	return n
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

func report(w io.Writer, c clank.Corpus, pop populations) {
	byClass := map[proposal.FailureClass]int{}
	for _, cs := range c.Cases {
		byClass[cs.FailureClass]++
	}
	_, _ = fmt.Fprintf(w, "mined %d cases from %d segments\n", len(c.Cases), len(c.Segments))
	_, _ = fmt.Fprintf(w, "%d reasoning-journal records observed (every terminal phase, not yet joined into cases)\n", pop.Journaled)
	total := pop.Labelled + pop.InFlight + pop.Unlabelled
	_, _ = fmt.Fprintf(w, "corpus: %d cases — %d labelled, %d in flight, %d unlabelled (declined; no verdict exists)\n",
		total, pop.Labelled, pop.InFlight, pop.Unlabelled)
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
	// A tagged artifact older than this build's layout (e.g. v2, pre-RunID)
	// falls through to the same decode below — fields it never had come back
	// as zero values, never reconstructed the way migrateLegacy recovers
	// SignalRef for the pre-tag layout.
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
