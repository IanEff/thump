// Package unseal reads sealed WAL segments back into legible text. It is the
// only reader for what every beat writes: a segment in the bucket is
// authenticated ciphertext, and without this the ProposalSet carrying a run's
// CausalScores, confidence and gate result is durable but unreadable.
//
// Read-only by construction — it opens files and prints. It reaches no cluster,
// no bucket, and no stream, so pulling the object is the operator's job and
// stays outside the blast radius of anything here.
package unseal

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/objectstore"
	"github.com/ianeff/thump/internal/sealbox"
)

// Main unseals each named segment file and prints one summary per boundary
// object. Returns 0 on success, 1 on any failure — the exit-code shape the
// other binaries in this repo use, and what makes it drivable from testscript.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unseal", flag.ContinueOnError)
	fs.SetOutput(stderr)
	raw := fs.Bool("raw", false, "print each decoded line verbatim instead of a ProposalSet summary")
	fs.Usage = func() {
		_, _ = fmt.Fprint(stderr, "usage: unseal [-raw] <segment file>...\n\n"+
			"Unseals WAL segments pulled from the object store and prints what they hold.\n"+
			"THUMP_SEAL_KEY must hold the same base64 key the beats seal with. Reading it\n"+
			"out of a cluster takes two decodes, not one:\n\n"+
			"  kubectl get secret thump-seal -n thump -o jsonpath='{.data.key}' | base64 -d\n\n"+
			"The inner value is itself base64; stopping after one decode yields a key of\n"+
			"plausible length that fails every open with an authentication error.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 1
	}

	key, err := KeyFromEnv(os.Getenv("THUMP_SEAL_KEY"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	for _, path := range fs.Args() {
		if err := printSegment(stdout, key, path, *raw); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	}
	return 0
}

// KeyFromEnv decodes the base64 seal key, rejecting a wrong length up front —
// a 31-byte key is a truncated paste, and diagnosing it here beats an
// authentication failure that looks identical to a corrupt segment.
func KeyFromEnv(env string) (sealbox.Key, error) {
	if env == "" {
		return sealbox.Key{}, errors.New("THUMP_SEAL_KEY is unset")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env))
	if err != nil {
		return sealbox.Key{}, fmt.Errorf("THUMP_SEAL_KEY is not base64: %w", err)
	}
	if len(b) != len(sealbox.Key{}) {
		return sealbox.Key{}, fmt.Errorf("THUMP_SEAL_KEY decodes to %d bytes, want %d — a value read straight out of kubectl needs a second base64 -d", len(b), len(sealbox.Key{}))
	}
	return sealbox.Key(b), nil
}

func printSegment(w io.Writer, key sealbox.Key, path string, raw bool) error {
	f, err := os.Open(path) //nolint:gosec // G304: operator-supplied segment path, not user input
	if err != nil {
		return fmt.Errorf("open segment: %w", err)
	}
	defer func() { _ = f.Close() }()

	lines, err := objectstore.UnsealSegment(key, f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	_, _ = fmt.Fprintf(w, "== %s (%d objects)\n", path, len(lines))
	for _, line := range lines {
		if raw {
			_, _ = fmt.Fprintf(w, "%s\n", line)
			continue
		}
		_, _ = fmt.Fprint(w, Summarize(line))
	}
	return nil
}

// Summarize renders one WAL line as a ProposalSet, falling back to the raw line
// when it holds some other boundary object — every beat's segments share this
// format, so a rattle segment decoded here is a wrong-subject mistake worth
// showing rather than an error worth stopping on.
func Summarize(line []byte) string {
	var set proposal.Set
	if err := json.Unmarshal(line, &set); err != nil || set.SignalRef == "" {
		return string(line) + "\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n-- %s  class=%s tier=%s\n", set.SignalRef, set.FailureClass, set.ServiceTier)
	if set.Gate != nil {
		fmt.Fprintf(&b, "   gate: passed=%t reason=%q\n", set.Gate.Passed, set.Gate.Reason)
	}
	if set.Status != nil {
		fmt.Fprintf(&b, "   status: %s %s\n", set.Status.Phase, set.Status.Reason)
	}

	for _, c := range set.Proposals {
		marker := " "
		if c.ID == set.Recommended {
			marker = "*"
		}
		fmt.Fprintf(&b, "  %s rank=%d %s confidence=%.3f blast=%s citations=%d\n",
			marker, c.Rank, c.ContractRef, c.Confidence, c.BlastTier, len(c.Citations))
	}

	// The causal scores are the reason this reader exists: InTopology false on
	// every row means the change source and the topology graph disagreed about
	// names, and no likelihood reached the confidence product.
	fmt.Fprintf(&b, "   causal: %d scores\n", len(set.CausalScores))
	for _, cs := range set.CausalScores {
		fmt.Fprintf(&b, "     %s inTopology=%t likelihood=%.3f temporal=%.3f topological=%.3f historical=%.3f\n",
			cs.EventID, cs.InTopology, cs.Likelihood, cs.Temporal, cs.Topological, cs.Historical)
	}
	return b.String()
}
