// Package pipeline chains the reasoning, governance, and actuation beats
// in-memory — a headless incident simulation that executes end-to-end without
// background daemons.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/step"
	"github.com/ianeff/thump/internal/thump"
)

// Result is the immutable end-to-end record of an incident simulation —
// captures every beat transition from initial detection to rendered actuation.
type Result struct {
	Detection signal.Detection  `json:"detection"` // initial incident signal loaded from disk
	Proposal  proposal.Set      `json:"proposal"`  // ranked proposals synthesized by reasoning
	Decision  decision.Governed `json:"decision"`  // policy-evaluated governance envelope
	Outcome   outcome.Outcome   `json:"outcome"`   // dry-run actuation outcome
	Duration  time.Duration     `json:"duration"`  // total simulation execution elapsed time
}

// Run executes an end-to-end incident simulation from a detection file through
// clank reasoning, hiss governance, and dry-run thump actuation — dry-run by
// default, never touching infrastructure unattended.
func Run(ctx context.Context, detectionFile, profileDir, modelName, apiKey string) (Result, error) {
	clankFn := func(ctx context.Context, detFile, profDir string) (proposal.Set, error) {
		return step.RunClank(ctx, detFile, profDir, modelName, apiKey)
	}
	return runPipeline(ctx, detectionFile, profileDir, clankFn)
}

func runWithModelAndTools(ctx context.Context, detectionFile, profileDir string, model reason.Model, tools map[string]reason.Tool) (Result, error) {
	clankFn := func(ctx context.Context, detFile, profDir string) (proposal.Set, error) {
		return step.RunClankWithModelAndTools(ctx, detFile, profDir, model, tools)
	}
	return runPipeline(ctx, detectionFile, profileDir, clankFn)
}

func runPipeline(ctx context.Context, detectionFile, profileDir string, clankFn func(context.Context, string, string) (proposal.Set, error)) (Result, error) {
	start := time.Now()

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if detectionFile == "" {
		return Result{}, errors.New("detection file is required")
	}
	if profileDir == "" {
		return Result{}, errors.New("profile directory is required")
	}

	data, err := os.ReadFile(detectionFile) //nolint:gosec // G304: operator-supplied detection file path, not user input
	if err != nil {
		return Result{}, fmt.Errorf("read detection file: %w", err)
	}

	var det signal.Detection
	if err := yaml.Unmarshal(data, &det); err != nil {
		return Result{}, fmt.Errorf("parse detection file: %w", err)
	}

	ps, err := clankFn(ctx, detectionFile, profileDir)
	if err != nil {
		return Result{}, fmt.Errorf("run clank: %w", err)
	}

	policyPath, err := findFile(
		filepath.Join(profileDir, "hiss", "policy.yaml"),
		filepath.Join(profileDir, "policy.yaml"),
		filepath.Join(profileDir, "..", "hiss", "policy.yaml"),
	)
	if err != nil {
		return Result{}, fmt.Errorf("locate policy: %w", err)
	}
	pol, err := hiss.LoadPolicy(policyPath)
	if err != nil {
		return Result{}, fmt.Errorf("load policy: %w", err)
	}

	auth := hiss.Authority{}
	d := auth.Evaluate(ps, pol, time.Now())
	gov := decision.Governed{Decision: d, Set: ps}

	catPath, err := findFile(
		filepath.Join(profileDir, "actions", "catalog.yaml"),
		filepath.Join(profileDir, "catalog.yaml"),
	)
	if err != nil {
		return Result{}, fmt.Errorf("locate catalog: %w", err)
	}
	cat, err := contract.LoadCatalogFile(catPath, contract.Preconditions)
	if err != nil {
		return Result{}, fmt.Errorf("load action catalog: %w", err)
	}

	order, err := (thump.Actuator{}).Render(gov, cat, time.Now())
	if err != nil {
		return Result{}, fmt.Errorf("render order: %w", err)
	}

	exec := thump.DryRun{}
	out := exec.Execute(ctx, order, time.Now())

	return Result{
		Detection: det,
		Proposal:  ps,
		Decision:  gov,
		Outcome:   out,
		Duration:  time.Since(start),
	}, nil
}

func findFile(paths ...string) (string, error) {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("file not found in candidates: %v", paths)
}
