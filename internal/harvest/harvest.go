package harvest

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/tlsx"
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
	sets    SetWatcher
}

func NewHarvest(w Watcher, r Runner, sw SetWatcher) *Harvest {
	return &Harvest{watcher: w, runner: r, sets: sw}
}

// Run fires one scenario end to end: preflight -> preconditions -> fault
// settle -> restore -> graded result.
// The returns are named because the deferred restore amends them: a restore
// failure discovered after the body has returned has nowhere else to go, and
// an unnamed return would drop it into a copy nobody reads.
func (h *Harvest) Run(ctx context.Context, sc Scenario) (res Result, err error) {
	res = Result{
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
		rerr := h.restore(ctx, sc)
		if rerr == nil {
			return
		}
		if res.Err != nil {
			res.Err = fmt.Errorf("%w (restore also failed: %w)", res.Err, rerr)
		} else {
			res.Err = fmt.Errorf("restore failed: %w", rerr)
		}
		err = res.Err
	}()

	if err := h.applyAction(ctx, sc.Fault); err != nil {
		res.Err = fmt.Errorf("fault: %w", err)
		return res, res.Err
	}

	o, err := Settle(ctx, h.watcher, sc.SignalRef, sc.SettleWindow)
	if err != nil {
		res.Err = err
		res.ActualResult = outcome.ResultUnknown
		return res, res.Err
	}

	// firstSetFor gets its own bounded context, the same shape Settle
	// builds for itself — by the time the outcome above has settled, the
	// Set that led to it was published seconds ago, but confidence
	// enrichment is a nice-to-have on Result, not a reason a whole harvest
	// run should hang forever if this particular lookup never resolves.
	setCtx, setCancel := context.WithTimeout(ctx, sc.SettleWindow)
	if s, ok := firstSetFor(setCtx, h.sets, sc.SignalRef); ok && len(s.Proposals) > 0 {
		top := s.Proposals[0]
		res.EmittedConfidence = top.Confidence
		res.ComputedConfidence = top.ComputedConfidence
		res.CeilingBound = top.ConfidenceCeilingBound
	}
	setCancel()

	res.ActualResult = o.Result
	if o.ObservedSeverity != nil {
		res.EmittedConfidence = *o.ObservedSeverity
	}

	return res, nil
}

// restore puts the rig back: the fault's own reversal, then every
// precondition's, in reverse order. It deliberately does not inherit the
// caller's cancellation — a harvest is most often stopped by a human losing
// patience, and a restore that inherits that cancellation reverts nothing
// while reporting nothing. Every step runs even when an earlier one failed;
// the errors join, because a failed fault reversal is no reason to leave
// mon_osd_down_out_interval at 60.
func (h *Harvest) restore(ctx context.Context, sc Scenario) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
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
	ScenarioName       string                `json:"scenarioName" yaml:"scenarioName"`
	ExpectedClass      proposal.FailureClass `json:"expectedClass" yaml:"expectedClass"`
	ExpectedContract   string                `json:"expectedContract" yaml:"expectedContract"`
	ExpectedVerdict    string                `json:"expectedVerdict" yaml:"expectedVerdict"` // approved, held, or declined
	ActualResult       outcome.Result        `json:"actualResult" yaml:"actualResult"`
	EmittedConfidence  float64               `json:"emittedConfidence" yaml:"emittedConfidence"`
	ComputedConfidence float64               `json:"computedConfidence"`
	CeilingBound       bool                  `json:"ceilingBound" yaml:"ceilingBound"`
	Err                error                 `json:"err" yaml:"err"`
}

// Main runs every row of --scenarios (optionally narrowed by --row) against
// a live cluster and NATS broker, printing one Result per run. It grades
// nothing: a Result's ExpectedX fields disagreeing with its ActualResult is
// calibration data for a human to read, the same posture tune's NotYet
// takes toward its own grid — this loop only reports whether a run's own
// execution (preconditions, fault, settle, restore) succeeded. Returns 0
// when every scenario's own execution completed without error; 1 if any
// did, or if the scenario table or the broker connection never came up.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("harvest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	scenariosPath := fs.String("scenarios", "", "path to the scenario table (required)")
	natsURL := fs.String("nats-url", "", "NATS URL to watch thump.outcomes/thump.proposals on (required)")
	certFile := fs.String("tls-cert", "", "client cert, required with --nats-url")
	keyFile := fs.String("tls-key", "", "client key, required with --nats-url")
	caFile := fs.String("tls-ca", "", "CA bundle, required with --nats-url")
	only := fs.String("row", "", "run only the scenario whose name contains this substring")
	asJSON := fs.Bool("json", false, "print each Result as JSON instead of a human line")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scenariosPath == "" || *natsURL == "" {
		_, _ = fmt.Fprintln(stderr, "usage: harvest --scenarios <path> --nats-url <url> [--tls-cert path --tls-key path --tls-ca path] [--row substring] [--json]")
		return 2
	}

	table, err := LoadScenarios(*scenariosPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "harvest:", err)
		return 1
	}

	ctx := context.Background()
	tc := tlsx.Config{CertFile: *certFile, KeyFile: *keyFile, CAFile: *caFile}
	js, closer, err := broker.Connect(ctx, *natsURL, tc, broker.Hooks{})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "harvest:", err)
		return 1
	}
	defer closer()

	h := NewHarvest(NewNATSWatcher(js), CommandRunner{}, NewNATSSetWatcher(js))

	failed := false
	for _, sc := range table.Scenarios {
		if *only != "" && !strings.Contains(sc.Name, *only) {
			continue
		}
		res, runErr := h.Run(ctx, sc)
		if runErr != nil {
			failed = true
		}
		if *asJSON {
			if err := json.NewEncoder(stdout).Encode(res); err != nil {
				_, _ = fmt.Fprintln(stderr, "harvest:", err)
				return 1
			}
			continue
		}
		printResult(stdout, res)
	}

	if failed {
		return 1
	}
	return 0
}

func printResult(w io.Writer, res Result) {
	status := "OK"
	if res.Err != nil {
		status = "ERR"
	}
	_, _ = fmt.Fprintf(w, "%-4s %-30s expected=%s/%s/%s actual=%s confidence=%.3f computed=%.3f ceiling=%t",
		status, res.ScenarioName, res.ExpectedClass, res.ExpectedContract, res.ExpectedVerdict,
		res.ActualResult, res.EmittedConfidence, res.ComputedConfidence, res.CeilingBound)
	if res.Err != nil {
		_, _ = fmt.Fprintf(w, " err=%q", res.Err)
	}
	_, _ = fmt.Fprintln(w)
}
