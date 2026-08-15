// Package probe is calipers probe's verb: fire N real reasoning runs against
// one captured detection, through clank's real evidence backends, with no
// publisher and no durable record. Every run is read-only against the
// cluster by construction, not by policy — clank.ProbeEngine never wires a
// Pub or a Journal, so there is nothing here that could reach the broker or
// the corpus.
package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/anthropic"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/config"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/evidence"
)

const modelRequestTimeout = 120 * time.Second

// Row is one probe run's result — the confidence terms that produced its
// number, not just the number, so a probe sample is conclusive without a
// transcript decrypt.
type Row struct {
	Run         int                      `json:"run"`
	Phase       string                   `json:"phase"`
	Reason      string                   `json:"reason,omitempty"`
	ContractRef string                   `json:"contractRef,omitempty"`
	Confidence  float64                  `json:"confidence,omitempty"`
	Computed    float64                  `json:"computed,omitempty"`
	Ceiling     bool                     `json:"ceiling,omitempty"`
	Terms       proposal.ConfidenceTerms `json:"terms,omitempty"`
	Citations   []string                 `json:"citations,omitempty"`
}

// rowFor reads run's Row from set — the recommended Candidate's own terms,
// or a bare phase/reason when the run never formed one.
func rowFor(run int, set proposal.Set) Row {
	row := Row{Run: run}
	if set.Status != nil {
		row.Phase = set.Status.Phase
		row.Reason = set.Status.Reason
	}
	for _, c := range set.Proposals {
		if c.ID != set.Recommended {
			continue
		}
		row.ContractRef = c.ContractRef
		row.Confidence = c.Confidence
		row.Computed = c.ComputedConfidence
		row.Ceiling = c.ConfidenceCeilingBound
		row.Terms = c.Terms
		row.Citations = c.Citations
		break
	}
	return row
}

// Run fires n independent Propose calls against det through eng, resetting
// eng's Ledger and Store before each one (clank.ProbeReset) so run 2..n never
// sees an earlier run's own set as an open dupe — n draws of the same
// question, not n-1 dedup collisions. eng is never wired to a Pub or a
// Journal by ProbeEngine, so nothing here reaches the broker or the corpus.
func Run(ctx context.Context, eng *clank.Engine, det signal.Detection, n int) ([]Row, error) {
	rows := make([]Row, 0, n)
	for i := 1; i <= n; i++ {
		clank.ProbeReset(eng)
		set, err := eng.Propose(ctx, det)
		if err != nil {
			return nil, fmt.Errorf("propose run %d: %w", i, err)
		}
		rows = append(rows, rowFor(i, set))
	}
	return rows, nil
}

// Main grades N live runs against one captured detection and prints one row
// per run. A missing ANTHROPIC_API_KEY is a clean skip returning 0, matching
// rca.Main's convention — never a hard failure on an operator machine with
// no key configured.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	detection := fs.String("detection", "", "path to a signal.Detection fixture, e.g. from task capture-detection (required)")
	runs := fs.Int("runs", 1, "number of independent reasoning runs to fire")
	asJSON := fs.Bool("json", false, "print one JSON row per run instead of a table")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *detection == "" || *runs < 1 {
		_, _ = fmt.Fprintln(stderr, "usage: probe -detection <path> [-runs N] [-json]")
		return 2
	}

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		_, _ = fmt.Fprintln(stderr, "ANTHROPIC_API_KEY unset - probe needs a real model; skipping")
		return 0
	}

	det, err := loadDetection(*detection)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe:", err)
		return 1
	}

	cfg, err := config.LoadProbe()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe:", err)
		return 1
	}
	cat, err := contract.LoadCatalogFile(cfg.ActionCatalog, contract.Preconditions)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe: load action catalog:", err)
		return 1
	}
	classes, err := contract.LoadFailureClassesFile(cfg.FailureClasses)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe: load failure classes:", err)
		return 1
	}
	weights, err := clank.LoadWeightsFile(cfg.Weights)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe: load weights:", err)
		return 1
	}
	limits, err := clank.LoadLimitsFile(cfg.Limits)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe: load limits:", err)
		return 1
	}

	var ev evidence.Config
	if cfg.EvidenceQueries != "" {
		ev, err = evidence.LoadEvidenceConfig(cfg.EvidenceQueries)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "probe: load evidence queries:", err)
			return 1
		}
	}

	// A probe run is always the offline (non-broker) posture: no TLS material
	// of its own, so backendTLS stays nil and PROM_URL/LOKI_URL are dialed in
	// the clear, same declared exception clank.Main's own offline path takes.
	kube, argo := clank.InClusterClients()
	model := anthropic.NewModel(key, modelRequestTimeout)

	var backendTLS *tls.Config
	clankCfg := clankConfig(cfg)
	eng, err := clank.ProbeEngine(clankCfg, backendTLS, ev, kube, argo, cat, classes, model, weights, limits)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe:", err)
		return 1
	}

	rows, err := Run(context.Background(), eng, det, *runs)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "probe:", err)
		return 1
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				_, _ = fmt.Fprintln(stderr, "probe:", err)
				return 1
			}
		}
		return 0
	}

	for _, r := range rows {
		ceiling := "-"
		if r.Ceiling {
			ceiling = "BOUND"
		}
		_, _ = fmt.Fprintf(stdout, "%-3d %-14s %-28s confidence=%.2f computed=%.2f ceiling=%-5s grounding=%.1f corroborated=%d %s\n",
			r.Run, r.Phase, r.ContractRef, r.Confidence, r.Computed, ceiling, r.Terms.Grounding, r.Terms.Corroborated, r.Reason)
	}
	_, _ = fmt.Fprintf(stdout, "\n%d probe run(s) against %s — read-only, nothing published or journaled\n", len(rows), *detection)
	return 0
}

// clankConfig lifts the fields ProbeEngine's buildTools/buildIntake read out
// of cfg into a config.Clank — probe's own environment carries none of
// Clank's inbox/outbox or BrokerStore fields, and buildTools/buildIntake
// never look at them, so the zero value for the rest is exactly right.
func clankConfig(cfg config.Probe) config.Clank {
	return config.Clank{
		PromURL:          cfg.PromURL,
		EvidenceQueries:  cfg.EvidenceQueries,
		LokiURL:          cfg.LokiURL,
		WhirCatalog:      cfg.WhirCatalog,
		WhirStateQueries: cfg.WhirStateQueries,
	}
}

// loadDetection reads path as a bare signal.Detection YAML file — the same
// format task capture-detection produces under
// internal/clank/testdata/detections/.
func loadDetection(path string) (signal.Detection, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied CLI flag, not user input
	if err != nil {
		return signal.Detection{}, fmt.Errorf("read detection %s: %w", path, err)
	}
	var det signal.Detection
	if err := yaml.Unmarshal(raw, &det); err != nil {
		return signal.Detection{}, fmt.Errorf("decode detection %s: %w", path, err)
	}
	return det, nil
}
