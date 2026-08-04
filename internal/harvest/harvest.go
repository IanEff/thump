package harvest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

const restoreTimeout = 2 * time.Minute

// Runner executes one shell-level step of a harvest: applying or deleting
// a fault manifest, exec'ing a chaos script, or running a raw precondition
// command.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// CommandRunner is the production Runner: os/exec, one process per
// call.
type CommandRunner struct{}

func (CommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: name/args are operator-authored scenario table entries, not user input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, ""), err, out)
	}
	return nil
}

type Harvest struct {
	watcher Watcher
	runner  Runner
}

func NewHarvest(w Watcher, r Runner) *Harvest {
	return &Harvest{watcher: w, runner: r}
}

// Run fires one scenario end to end: preflight -> preconditions -> fault
// settle -> restore -> graded result.
func (h *Harvest) Run(ctx context.Context, sc Scenario) (Result, error) {
	res := Result{
		ScenarioName:     sc.Name,
		ExpectedClass:    sc.Expects.FailureClass,
		ExpectedContract: sc.Expects.ContractRef,
		ExpectedVerdict:  sc.Expects.Verdict,
	}

	// Preconditions run in declared order; restore always runs, even on
	// failure.
	for _, p := range sc.Preconditions {
		if err := h.runAction(ctx, p.Set); err != nil {
			res.Err = fmt.Errorf("precondition %s: %w", p.Name, err)
			return res, res.Err
		}
	}
	defer func() {
		if err := h.restore(ctx, sc); err != nil {
			if res.Err != nil {
				res.Err = fmt.Errorf("%w (restore also failed: %w)", res.Err, err)
			} else {
				res.Err = fmt.Errorf("restore failed: %w", err)
			}
		}
	}()

	if err := h.applyAction(ctx, sc.Fault); err != nil {
		res.Err = fmt.Errorf("fault: %w", err)
		return res, res.Err
	}

	fingerprint := sc.Domain + ":" + sc.Name // TODO: placeholder join key

	o, err := Settle(ctx, h.watcher, fingerprint, sc.SettleWindow)
	if err != nil {
		res.Err = err
		res.ActualResult = outcome.ResultUnknown
		return res, res.Err
	}

	res.ActualResult = o.Result
	if o.ObservedSeverity != nil {
		res.EmittedConfidence = *o.ObservedSeverity
	}

	return res, nil
}

func (h *Harvest) restore(ctx context.Context, sc Scenario) error {
	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()

	var errs []error
	if err := h.applyAction(ctx, sc.Restore); err != nil {
		errs = append(errs, fmt.Errorf("fault restore: %w", err))
	}
	for i := len(sc.Preconditions) - 1; i >= 0; i-- {
		p := sc.Preconditions[i]
		if err := h.runAction(ctx, p.Restore); err != nil {
			errs = append(errs, fmt.Errorf("precondition %s restore: %w", p.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (h *Harvest) applyAction(ctx context.Context, a Action) error {
	switch a.Apply {
	case "kubectl":
		return h.runner.Run(ctx, "kubectl", append([]string{"apply", "-f", a.Path}, a.Args...)...)
	case "kubectl-delete":
		return h.runner.Run(ctx, "kubectl", append([]string{"delete", "-f", a.Path}, a.Args...)...)
	case "exec":
		return h.runner.Run(ctx, a.Path, a.Args...)
	default:
		return fmt.Errorf("harvest: unknown apply verb: %q", a.Apply)
	}
}

func (h *Harvest) runAction(ctx context.Context, command string) error {
	return h.runner.Run(ctx, "/bin/sh", "-c", command)
}

type Result struct {
	ScenarioName      string                `json:"scenarioName" yaml:"scenarioName"`
	ExpectedClass     proposal.FailureClass `json:"expectedClass" yaml:"expectedClass"`
	ExpectedContract  string                `json:"expectedContract" yaml:"expectedContract"`
	ExpectedVerdict   string                `json:"expectedVerdict" yaml:"expectedVerdict"` // approved, held, or declined
	ActualResult      outcome.Result        `json:"actualResult" yaml:"actualResult"`
	EmittedConfidence float64               `json:"emittedConfidence" yaml:"emittedConfidence"`
	CeilingBound      bool                  `json:"ceilingBound" yaml:"ceilingBound"`
	Err               error                 `json:"err" yaml:"err"`
}
