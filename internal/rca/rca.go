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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/ianeff/thump/internal/anthropic"
	"github.com/ianeff/thump/internal/clank"
)

const modelRequestTimeout = 120 * time.Second

// Main grades the suite and prints one line per row. A missing
// ANTHROPIC_API_KEY is a clean skip returning 0, never a failure, so `task
// rca` is safe to run anywhere without a key configured.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rca", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the report as JSON")
	transcripts := fs.String("transcripts", "", "directory to write reason-loop transcripts to")
	only := fs.String("row", "", "grade only the row whose name contains this substring")
	rig := fs.String("rig", "thump-test", "config/<rig>/whir profile to grade evidence queries against")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		_, _ = fmt.Fprintln(stderr, "ANTHROPIC_API_KEY unset - the RCA harness needs a real model; skipping")
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

	// AnthropicModel is the only Model production selects; GeminiModel has
	// no caller yet, so grading against it would score a model the shipped
	// engine never runs.
	model := anthropic.NewModel(key, modelRequestTimeout)

	// config/clank/weights.yaml, not DefaultScoringWeights: the two agree
	// today, but nothing enforces that, and a suite that grades a value
	// with no obligation to track the shipped file can pass while the
	// deployed weights have drifted out from under it.
	weights, err := clank.LoadWeightsFile(configPath("clank", "weights.yaml"))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rca:", err)
		return 1
	}

	ctx := context.Background()

	// The graded suite's kube fake needs one topology object to resolve the
	// evidence-queries.yaml selectors against — a Ceph pod, because that's
	// the rig the eight detection fixtures were captured on. newHarness
	// itself carries no Ceph knowledge; the fixture lives here, at the
	// composition root, not inside the instrument.
	kubeObjects := []runtime.Object{&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rook-ceph-mon-a",
			Namespace: "rook-ceph",
			Labels:    map[string]string{"app": "rook-ceph-mon"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}}

	var rows []Row
	for _, c := range Table() {
		if *only != "" && !strings.Contains(c.Name, *only) {
			continue
		}
		row, err := RunCase(ctx, c, model, weights, dir, *rig, kubeObjects...)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "rca:", err)
			return 1
		}
		rows = append(rows, row)
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
				status, r.Name, r.Class, r.Computed, r.Emitted, ceiling, r.Miss)
		}
		_, _ = fmt.Fprintf(stdout, "\nscored %d/%d, floor %d\n", rep.Scored, len(rep.Rows), rep.Floor)
	}

	if !rep.Met() {
		return 1
	}
	return 0
}
