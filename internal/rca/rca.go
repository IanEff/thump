package rca

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/modelsel"
	"github.com/ianeff/thump/internal/probe"
)

// Main grades the suite and prints one line per row. A missing key for the
// selected -model is a clean skip returning 0, never a failure, so `task
// rca` is safe to run anywhere without a key configured.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rca", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	transcripts := fs.String("transcripts", "", "directory to write reason-loop transcripts to")
	only := fs.String("row", "", "grade only the row whose name contains this substring")
	rig := fs.String("rig", "", "config/<rig> to grade catalog, failure classes, evidence queries, and kube objects against (required)")
	modelName := fs.String("model", "haiku", "reasoning backend to grade: haiku (production default), sonnet, or gemini-low")
	runs := fs.Int("runs", 1, "independent draws per row; > 1 prints a confidence/corroboration spread alongside the pass/fail table")
	floor := fs.Float64("floor", 0.75, "confidence floor a run must clear to count as underFloor=0 in the -runs summary; purely a grading label printed there, never enforced")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *rig == "" {
		_, _ = fmt.Fprintln(stderr, "usage: rca -rig <name> [-json] [-transcripts dir] [-row substring] [-model haiku|sonnet|gemini-low] [-runs N] [-floor F]")
		return 2
	}

	ctx := context.Background()

	// -model measures a comparison offline, on scripted evidence held
	// byte-identical across every draw — it makes no claim about what
	// production selects, which is AnthropicModel/haiku alone.
	model, skip, err := modelsel.For(ctx, *modelName)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rca:", err)
		return 1
	}
	if skip != "" {
		_, _ = fmt.Fprintln(stderr, skip+" unset - the RCA harness needs a real model; skipping")
		return 0
	}

	dir := *transcripts
	if dir == "" {
		dir = os.Getenv("CLANK_RCA_TRANSCRIPTS")
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "clank-rca-transcripts")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // G703: dir comes from a CLI flag, env var, or os.TempDir — operator-supplied, not user input
		_, _ = fmt.Fprintln(stderr, "rca:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stderr, "transcripts (read a row's own directory when it misses):", dir)

	// config/clank/weights.yaml, not DefaultScoringWeights: the graded suite
	// should score the value production actually loads, not a copy of it.
	// TestDefaultScoringWeights_MatchesTheShippedConfig (internal/clank)
	// enforces that the two stay equal, so a suite using either here can't
	// silently drift from the deployed weights.
	weights, err := clank.LoadWeightsFile(configPath("clank", "weights.yaml"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rca:", err)
		return 1
	}

	kubeObjects, err := loadKubeObjects(configPath(*rig, "rca", "kube-objects.yaml"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rca:", err)
		return 1
	}

	var rows []Row
	for _, c := range Table() {
		if c.Rig != *rig {
			continue
		}
		if *only != "" && !strings.Contains(c.Name, *only) {
			continue
		}
		var samples []probe.Sample
		for i := 0; i < *runs; i++ {
			row, err := RunCase(ctx, c, model, weights, dir, *rig, kubeObjects...)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "rca:", err)
				return 1
			}
			rows = append(rows, row)
			samples = append(samples, row.sample())
		}
		if *runs > 1 {
			_, _ = fmt.Fprintln(stdout, c.Name+":")
			probe.RenderSummary(stdout, probe.Summarize(samples, *floor))
		}
	}

	rep := NewReport(rows)

	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(rep); err != nil {
			_, _ = fmt.Fprintln(stderr, "rca:", err)
			return 1
		}
	} else {
		for _, r := range rep.Rows {
			status := "MISS"
			if r.Pass {
				status = "PASS"
			} else if r.KnownMiss {
				status = "KNOWN"
			}
			ceiling := "-"
			if r.CeilingBound {
				ceiling = "BOUND"
			}
			_, _ = fmt.Fprintf(stdout, "%-5s %-58s class=%-20s computed=%.2f emitted=%.2f ceiling=%s %s\n",
				status, r.Name, r.Class, r.Computed, r.Confidence, ceiling, r.Miss)
		}
		_, _ = fmt.Fprintf(stdout, "\nscored %d/%d, floor %d\n", rep.Scored, len(rep.Rows), rep.Floor)
	}

	if !rep.Met() {
		return 1
	}
	return 0
}
