// Package step is the isolated beat execution plane: pure entrypoints that
// run each beat on static files without background daemons or cluster
// connectivity.
package step

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"

	sdk_anthropic "github.com/anthropics/anthropic-sdk-go"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/anthropic"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/rattle"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/thump"
)

// ErrNoDetection indicates that a rattle reconciliation pass completed
// without triggering any detection rules.
var ErrNoDetection = errors.New("no signal detected")

// RunHiss evaluates one proposal against policy and returns the governed
// decision envelope — hiss's pure authority pass over static files.
func RunHiss(ctx context.Context, proposalFile, policyFile string) (decision.Governed, error) {
	if ctx.Err() != nil {
		return decision.Governed{}, ctx.Err()
	}
	if proposalFile == "" {
		return decision.Governed{}, errors.New("proposal file is required")
	}
	if policyFile == "" {
		return decision.Governed{}, errors.New("policy file is required")
	}

	data, err := os.ReadFile(proposalFile) //nolint:gosec // G304: operator-supplied proposal file path, not user input
	if err != nil {
		return decision.Governed{}, fmt.Errorf("read proposal file: %w", err)
	}

	var ps proposal.Set
	if err := yaml.Unmarshal(data, &ps); err != nil {
		return decision.Governed{}, fmt.Errorf("parse proposal file: %w", err)
	}

	pol, err := hiss.LoadPolicy(policyFile)
	if err != nil {
		return decision.Governed{}, fmt.Errorf("load policy: %w", err)
	}

	auth := hiss.Authority{}
	d := auth.Evaluate(ps, pol, time.Now())

	return decision.Governed{Decision: d, Set: ps}, nil
}

// RunThump renders and optionally executes one governed decision against the
// action catalog — dry-run by default, never touching infrastructure
// unattended.
func RunThump(ctx context.Context, decisionFile, catalogFile string, dryRun bool) (outcome.Outcome, error) {
	if ctx.Err() != nil {
		return outcome.Outcome{}, ctx.Err()
	}
	if decisionFile == "" {
		return outcome.Outcome{}, errors.New("decision file is required")
	}
	if catalogFile == "" {
		return outcome.Outcome{}, errors.New("catalog file is required")
	}

	data, err := os.ReadFile(decisionFile) //nolint:gosec // G304: operator-supplied decision file path, not user input
	if err != nil {
		return outcome.Outcome{}, fmt.Errorf("read decision file: %w", err)
	}

	var g decision.Governed
	if err := yaml.Unmarshal(data, &g); err != nil {
		return outcome.Outcome{}, fmt.Errorf("parse decision file: %w", err)
	}

	cat, err := contract.LoadCatalogFile(catalogFile, contract.Preconditions)
	if err != nil {
		return outcome.Outcome{}, fmt.Errorf("load action catalog: %w", err)
	}

	order, err := (thump.Actuator{}).Render(g, cat, time.Now())
	if err != nil {
		return outcome.Outcome{}, fmt.Errorf("render order: %w", err)
	}

	if dryRun {
		exec := thump.DryRun{}
		return exec.Execute(ctx, order, time.Now()), nil
	}

	runner, err := actuate.New(cat, nil)
	if err != nil {
		return outcome.Outcome{}, fmt.Errorf("build live executor: %w", err)
	}
	exec := thump.Live{Runner: runner}
	return exec.Execute(ctx, order, time.Now()), nil
}

// RunClank reasons over a detection to produce an evidence-backed proposal
// set — the autonomous loop bounded by the action catalog.
func RunClank(ctx context.Context, detectionFile, profileDir, modelName, apiKey string) (proposal.Set, error) {
	if ctx.Err() != nil {
		return proposal.Set{}, ctx.Err()
	}
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return proposal.Set{}, errors.New("anthropic API key is required")
	}
	var m sdk_anthropic.Model
	switch modelName {
	case "", "haiku":
		m = anthropic.ModelClaudeHaiku4_5
	case "sonnet":
		m = anthropic.ModelClaudeSonnet5
	default:
		m = sdk_anthropic.Model(modelName)
	}
	model := anthropic.NewModel(apiKey, m, 120*time.Second)
	return runClank(ctx, detectionFile, profileDir, model)
}

func runClank(ctx context.Context, detectionFile, profileDir string, model reason.Model) (proposal.Set, error) {
	return runClankWithTools(ctx, detectionFile, profileDir, model, nil)
}

func runClankWithTools(ctx context.Context, detectionFile, profileDir string, model reason.Model, overrideTools map[string]reason.Tool) (proposal.Set, error) {
	if ctx.Err() != nil {
		return proposal.Set{}, ctx.Err()
	}
	if detectionFile == "" {
		return proposal.Set{}, errors.New("detection file is required")
	}
	if profileDir == "" {
		return proposal.Set{}, errors.New("profile directory is required")
	}

	data, err := os.ReadFile(detectionFile) //nolint:gosec // G304: operator-supplied detection file path, not user input
	if err != nil {
		return proposal.Set{}, fmt.Errorf("read detection file: %w", err)
	}

	var det signal.Detection
	if err := yaml.Unmarshal(data, &det); err != nil {
		return proposal.Set{}, fmt.Errorf("parse detection file: %w", err)
	}

	catPath, err := findFile(
		filepath.Join(profileDir, "actions", "catalog.yaml"),
		filepath.Join(profileDir, "catalog.yaml"),
	)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("locate catalog: %w", err)
	}
	cat, err := contract.LoadCatalogFile(catPath, contract.Preconditions)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load action catalog: %w", err)
	}

	classesPath, err := findFile(
		filepath.Join(profileDir, "actions", "failure-classes.yaml"),
		filepath.Join(profileDir, "failure-classes.yaml"),
	)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("locate failure classes: %w", err)
	}
	classes, err := contract.LoadFailureClassesFile(classesPath)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load failure classes: %w", err)
	}

	weightsPath, err := findFile(
		filepath.Join(profileDir, "clank", "weights.yaml"),
		filepath.Join(profileDir, "weights.yaml"),
		filepath.Join(profileDir, "..", "clank", "weights.yaml"),
	)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("locate weights: %w", err)
	}
	weights, err := clank.LoadWeightsFile(weightsPath)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load weights: %w", err)
	}

	limitsPath, err := findFile(
		filepath.Join(profileDir, "clank", "limits.yaml"),
		filepath.Join(profileDir, "limits.yaml"),
		filepath.Join(profileDir, "..", "clank", "limits.yaml"),
	)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("locate limits: %w", err)
	}
	limits, err := clank.LoadLimitsFile(limitsPath)
	if err != nil {
		return proposal.Set{}, fmt.Errorf("load limits: %w", err)
	}

	tools := overrideTools
	if tools == nil {
		tools = make(map[string]reason.Tool)
		evPath, err := findFile(
			filepath.Join(profileDir, "whir", "evidence-queries.yaml"),
			filepath.Join(profileDir, "evidence-queries.yaml"),
		)
		if err == nil {
			if ev, err := evidence.LoadEvidenceConfig(evPath); err == nil {
				if promURL := os.Getenv("PROM_URL"); promURL != "" && len(ev.Queries) > 0 {
					tools["metrics"] = &evidence.MetricsTool{BaseURL: promURL, Queries: ev.Queries}
				}
				if lokiURL := os.Getenv("LOKI_URL"); lokiURL != "" {
					tools["loki"] = &evidence.LokiTool{BaseURL: lokiURL, Subjects: ev.Index}
				}
			}
		}
	}

	ledger := clank.NewMemProposalLog()
	ledger.LedgerRetention = limits.LedgerRetention
	cases := clank.NewCaseBase()
	cases.MaxCases = limits.MaxCases

	eng := &clank.Engine{
		Intake:         clank.NewIntake(noopTopology{}, noopChange{}),
		Model:          model,
		Tools:          tools,
		Catalog:        cat,
		FailureClasses: classes,
		Ranker:         clank.NewRanker(),
		Store:          clank.NewMemStore(),
		Scorer:         &clank.CausalScorerImpl{Prior: cases},
		Prior:          cases,
		Ledger:         ledger,
		Gate:           clank.ReadinessGate{},
		MaxSteps:       limits.MaxSteps,
		Weights:        weights,
	}

	return eng.Propose(ctx, det)
}

func findFile(paths ...string) (string, error) {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("file not found in candidates: %v", paths)
}

type noopTopology struct{}

func (noopTopology) Topology(_ context.Context, _ signal.Detection) (proposal.TopologySnapshot, error) {
	return proposal.TopologySnapshot{}, nil
}

type noopChange struct{}

func (noopChange) Changes(_ context.Context, _ signal.Detection) (proposal.ChangeSnapshot, error) {
	return proposal.ChangeSnapshot{}, nil
}

// RunRattle reconciles configured SLOs against Prometheus telemetry to yield
// the first fired signal detection — rattle's evaluation pass in isolation.
func RunRattle(ctx context.Context, watchFile, queryConfigFile, promURL string) (signal.Detection, error) {
	if ctx.Err() != nil {
		return signal.Detection{}, ctx.Err()
	}
	if watchFile == "" {
		return signal.Detection{}, errors.New("watch file is required")
	}
	if queryConfigFile == "" {
		return signal.Detection{}, errors.New("query config file is required")
	}
	if promURL == "" {
		return signal.Detection{}, errors.New("promURL is required")
	}

	slos, err := rattle.LoadWatch(watchFile)
	if err != nil {
		return signal.Detection{}, fmt.Errorf("load watch file: %w", err)
	}

	query, err := rattle.LoadQueryConfig(queryConfigFile)
	if err != nil {
		return signal.Detection{}, fmt.Errorf("load query config: %w", err)
	}

	src := rattle.NewPromSource(promURL)
	src.Step = query.Step
	src.Window = query.Window

	r := &rattle.Reconciler{
		SLOs:      slos,
		Source:    src,
		Detector:  rattle.AccelerationDetector{Threshold: 0.5},
		Sustained: &rattle.SustainedBurnDetector{Threshold: 1.0, MinSamples: query.SustainedMinSamples},
		Debounce:  rattle.NewDebouncer(query.Debounce),
		Contract: &rattle.SignalContract{
			FreshnessBound:  query.FreshnessBound,
			ConfidenceFloor: 0.5,
		},
	}

	detections, err := r.Reconcile(ctx)
	if err != nil {
		return signal.Detection{}, fmt.Errorf("reconcile: %w", err)
	}

	if len(detections) == 0 {
		return signal.Detection{}, ErrNoDetection
	}

	return detections[0], nil
}
