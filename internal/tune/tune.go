package tune

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// deadKnobLimitation is printed first, every run, because a sweep over
// groundingMany and causal would look authoritative and isn't: no recorded
// row corroborates on two backends, so LikelihoodOK is structurally false in
// this harness and both surfaces are flat.
const deadKnobLimitation = "groundingMany and causal are not swept: no recorded row corroborates on two backends and LikelihoodOK is structurally false in this harness, so both surfaces are flat."

// Main sweeps GroundingNone and GroundingOne over recorded transcripts and
// prints the grid beside a NotYet. It never writes
// config/clank/weights.yaml or config/hiss/policy.yaml, and there is no flag
// that would: --apply is refused by leaving it unregistered, so
// flag.ContinueOnError's unknown-flag error is what turns it away.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tune", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	transcripts := fs.String("transcripts", "", "directory holding paired .jsonl/.set.json transcript fixtures")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *transcripts == "" {
		return emit(stdout, *asJSON, nil, NotYet{Reason: "no --transcripts directory given"})
	}

	pairs, err := findTranscripts(*transcripts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if len(pairs) == 0 {
		return emit(stdout, *asJSON, nil, NotYet{Reason: fmt.Sprintf("%s holds no paired .jsonl/.set.json transcripts", *transcripts)})
	}

	points, err := Run(context.Background(), SweepConfig{Transcripts: pairs})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	return emit(stdout, *asJSON, points, decide(points))
}

// findTranscripts pairs every foo.jsonl in dir with a foo.set.json beside
// it. A .jsonl with no matching .set.json is skipped rather than erroring —
// truncated.jsonl in the replay fixtures has no set for exactly this reason,
// since the exhaustion path it exercises fires before the engine ever needs
// one. A paired transcript that still fails to replay is not filtered here;
// Run drops it after a failed probe, so one broken fixture doesn't abort the
// whole sweep.
func findTranscripts(dir string) ([]TranscriptPaths, error) {
	entries, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("tune: read %s: %w", dir, err)
	}

	var pairs []TranscriptPaths
	for _, jsonl := range entries {
		setPath := strings.TrimSuffix(jsonl, ".jsonl") + ".set.json"
		if _, err := os.Stat(setPath); err != nil {
			continue
		}
		pairs = append(pairs, TranscriptPaths{JSONL: jsonl, Set: setPath})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].JSONL < pairs[j].JSONL })
	return pairs, nil
}

// decide reports NotYet, always. MeanConfidence rises with GroundingOne by
// construction — picking the grid point with the highest confidence would
// always recommend saturating the weight, which is not a finding about
// correctness. A real Proposal needs Grounded and Support evaluated against
// held-out outcomes, which Run does not compute; until a corpus is wired in,
// the grid is a human's reading material, not a verdict.
func decide(points []Point) NotYet {
	moved := 0
	for _, p := range points {
		if p.Moved > 0 {
			moved++
		}
	}
	if moved == 0 {
		return NotYet{Reason: "no point in the swept grid changed any transcript's top confidence; the corpus cannot distinguish these weights yet"}
	}
	return NotYet{Reason: fmt.Sprintf("%d of %d grid points moved a transcript's confidence, but mean confidence tracks groundingOne by construction — a Proposal needs Grounded/Support scored against held-out outcomes, not raw confidence", moved, len(points))}
}

func emit(stdout io.Writer, asJSON bool, points []Point, result NotYet) int {
	if asJSON {
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(struct {
			Limitation string  `json:"limitation"`
			Points     []Point `json:"points,omitempty"`
			Result     NotYet  `json:"result"`
		}{Limitation: deadKnobLimitation, Points: points, Result: result})
		return 0
	}

	_, _ = fmt.Fprintln(stdout, deadKnobLimitation)
	_, _ = fmt.Fprintln(stdout, "objective: Grounded = rca.Report.Scored, Support = Corpus.FloorSupport(class, floor)")
	for _, p := range points {
		_, _ = fmt.Fprintf(stdout, "  groundingNone=%.1f groundingOne=%.1f meanConfidence=%.3f moved=%d\n",
			p.GroundingNone, p.GroundingOne, p.MeanConfidence, p.Moved)
	}
	_, _ = fmt.Fprintf(stdout, "not yet: %s\n", result.Reason)
	return 0
}
