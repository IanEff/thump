package tune

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ianeff/thump/internal/rca"
)

// deadKnobLimitation is printed first, every run, because a sweep over
// groundingMany and causal would look authoritative and isn't: two backends
// are wired but no graded row cites a second one, and LikelihoodOK is
// structurally false in this harness, so both surfaces are flat.
const deadKnobLimitation = "groundingMany is not swept: two backends are wired, but no graded row cites a second one, and only a cited backend raises the tier — so the surface is flat. causal is not swept: LikelihoodOK is structurally false in this harness."

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
		return emit(stdout, *asJSON, 0, nil, Proposal{}, NotYet{Reason: "no --transcripts directory given"})
	}

	pairs, err := findTranscripts(*transcripts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if len(pairs) == 0 {
		return emit(stdout, *asJSON, 0, nil, Proposal{}, NotYet{Reason: fmt.Sprintf("%s holds no paired .jsonl/.set.json transcripts", *transcripts)})
	}

	cases := rca.Table()
	points, err := Run(context.Background(), SweepConfig{Transcripts: pairs, Cases: cases, Objective: DefaultObjective()})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	prop, notYet := decide(points)

	return emit(stdout, *asJSON, len(cases), points, prop, notYet)
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

// onGroundingNone filters the grid to the points sharing one GroundingNone
// value. Step 0's census read Corroborated straight off the fixtures and
// found GroundingNone never changes it, so decide reads Grounded purely
// along GroundingOne and holds GroundingNone fixed at the grid's first value.
func onGroundingNone(points []Point, none float64) []Point {
	var band []Point
	for _, p := range points {
		if p.GroundingNone == none {
			band = append(band, p)
		}
	}
	sort.Slice(band, func(i, j int) bool { return band[i].GroundingOne < band[j].GroundingOne })
	return band
}

// longestRunAtMax returns the longest contiguous stretch of band sharing its
// highest Grounded value — the bracket. band must already be sorted by
// GroundingOne.
func longestRunAtMax(band []Point) []Point {
	peak := 0
	for _, p := range band {
		if p.Grounded > peak {
			peak = p.Grounded
		}
	}

	var best, cur []Point
	for _, p := range band {
		if p.Grounded != peak {
			cur = nil
			continue
		}
		cur = append(cur, p)
		if len(cur) > len(best) {
			best = cur
		}
	}
	return best
}

// decide finds the widest contiguous run of GroundingOne holding the grid's
// maximum Grounded count and recommends its midpoint. Grounded is bounded on
// both sides by the labels themselves — below WantConfidenceAtLeast, above
// wherever a row's ConfidenceCeilingBound flips true — so a bracket is a
// finding; a bare maximum, which is what MeanConfidence gave three phases
// running, never was.
func decide(points []Point) (Proposal, NotYet) {
	if len(points) == 0 {
		return Proposal{}, NotYet{Reason: "no swept grid to decide over"}
	}

	band := onGroundingNone(points, points[0].GroundingNone)
	best := longestRunAtMax(band)

	switch {
	case best[0].Grounded == 0:
		return Proposal{}, NotYet{Reason: "no point in the swept grid grounds a labelled row; the corpus cannot distinguish these weights yet"}
	case len(best) == len(band):
		return Proposal{}, NotYet{Reason: fmt.Sprintf("every point in the swept grid grounds %d labelled rows; the corpus cannot distinguish these weights yet", best[0].Grounded)}
	}

	lo, hi := best[0].GroundingOne, best[len(best)-1].GroundingOne
	return Proposal{
		File:  "config/clank/weights.yaml",
		Key:   "groundingOne",
		To:    (lo + hi) / 2,
		Basis: basisFor(band, best),
	}, NotYet{}
}

// basisFor names the transitions bounding the bracket — where Grounded
// climbs into the plateau, and where it falls back out, if the grid ever
// shows that edge. Point carries only the aggregate Grounded count, not
// which row moved it; naming a row by name would mean threading rca.Row
// through the grid, which nothing needs yet.
func basisFor(band, best []Point) string {
	lo, hi := best[0].GroundingOne, best[len(best)-1].GroundingOne
	basis := fmt.Sprintf("grounded=%d holds for groundingOne in [%.2f, %.2f]", best[0].Grounded, lo, hi)

	loIdx := slices.Index(band, best[0])
	if loIdx > 0 {
		basis += fmt.Sprintf("; below %.2f, grounded falls to %d", lo, band[loIdx-1].Grounded)
	}

	hiIdx := slices.Index(band, best[len(best)-1])
	if hiIdx < len(band)-1 {
		basis += fmt.Sprintf("; above %.2f, grounded falls to %d", hi, band[hiIdx+1].Grounded)
	} else {
		basis += fmt.Sprintf("; the grid ends at %.2f with no ceiling found in range", hi)
	}
	return basis
}

func emit(stdout io.Writer, asJSON bool, cases int, points []Point, prop Proposal, result NotYet) int {
	if asJSON {
		enc := json.NewEncoder(stdout)
		_ = enc.Encode(struct {
			Limitation string   `json:"limitation"`
			Points     []Point  `json:"points,omitempty"`
			Proposal   Proposal `json:"proposal"`
			Result     NotYet   `json:"result"`
		}{Limitation: deadKnobLimitation, Points: points, Proposal: prop, Result: result})
		return 0
	}

	_, _ = fmt.Fprintln(stdout, deadKnobLimitation)
	for _, p := range points {
		_, _ = fmt.Fprintf(stdout, "  groundingNone=%.1f groundingOne=%.1f grounded=%d/%d meanConfidence=%.3f moved=%d\n",
			p.GroundingNone, p.GroundingOne, p.Grounded, cases, p.MeanConfidence, p.Moved)
	}
	if result.Reason != "" {
		_, _ = fmt.Fprintf(stdout, "not yet: %s\n", result.Reason)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "propose %s in %s: %.2f — %s\n", prop.Key, prop.File, prop.To, prop.Basis)
	return 0
}
