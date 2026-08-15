package harvest

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/tlsx"
)

const (
	restoreTimeout      = 2 * time.Minute
	defaultRefusalGrace = 3 * time.Minute
	defaultCooldown     = 10 * time.Minute
)

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

// kubeContextChecker reports kubectl's active context — production wiring
// shells out to kubectl; a test injects a stub instead of needing a real
// kubeconfig.
type kubeContextChecker func(ctx context.Context) (string, error)

func kubectlCurrentContext(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "kubectl", "config", "current-context").Output() //nolint:gosec // G204: fixed argv, no operator input
	if err != nil {
		return "", fmt.Errorf("kubectl config current-context: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// verifyKubeContext refuses to fire against a cluster nobody asked for.
// scenarios.yaml's own rig field is read only by an offline test
// (TestScenarios_WaitOnFingerprintsTheRigsWatchListCanActuallyProduce) —
// nothing else stops a scenario firing at whatever context happens to be
// current.
func verifyKubeContext(ctx context.Context, want string, check kubeContextChecker) error {
	got, err := check(ctx)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("active kubectl context is %q, want %q — switch context or drop --kube-context to fire without this check", got, want)
	}
	return nil
}

type Harvest struct {
	legs         Legs
	runner       Runner
	refusalGrace time.Duration
}

// NewHarvest builds a Harvest. refusalGrace of zero uses defaultRefusalGrace
// — the window Settle waits after a detection for a proposal.Set to appear
// on legs.Sets before calling the row refused.
func NewHarvest(legs Legs, r Runner, refusalGrace time.Duration) *Harvest {
	if refusalGrace <= 0 {
		refusalGrace = defaultRefusalGrace
	}
	return &Harvest{legs: legs, runner: r, refusalGrace: refusalGrace}
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
		StartedAt:        time.Now(),
	}

	// The defer is registered before the preconditions loop runs, not
	// after: a precondition failing partway through still needs every
	// prior precondition undone, and each restore command is idempotent
	// (sets a known-good value) so running one for a step that never
	// applied is harmless.
	defer func() {
		res.EndedAt = time.Now()
		rerr := h.restore(ctx, sc)
		if rerr == nil {
			return
		}
		if err != nil {
			err = fmt.Errorf("%w (restore also failed: %w)", err, rerr)
		} else {
			err = fmt.Errorf("restore failed: %w", rerr)
		}
		res.Err = err.Error()
	}()

	// Preconditions run in declared order; restore always runs, even on
	// failure.
	for _, p := range sc.Preconditions {
		if perr := h.runAction(ctx, p.Set); perr != nil {
			walkErr := fmt.Errorf("precondition %s: %w", p.Name, perr)
			res.Err = walkErr.Error()
			return res, walkErr
		}
	}

	if ferr := h.applyAction(ctx, sc.Fault); ferr != nil {
		walkErr := fmt.Errorf("fault: %w", ferr)
		res.Err = walkErr.Error()
		return res, walkErr
	}

	term, serr := Settle(ctx, h.legs, sc.SignalRef, sc.SettleWindow, h.refusalGrace)
	if serr != nil {
		res.Err = serr.Error()
		res.ActualResult = outcome.ResultUnknown
		return res, serr
	}
	res.ActualVerdict = term.Verdict
	res.ActualContract = term.ContractRef
	res.ActualResult = term.Result

	// A refused row published no Set by definition — the lookup below would
	// only burn the rest of the settle window waiting for one that can never
	// arrive.
	if term.Verdict != "refused" {
		// firstSetFor gets its own bounded context, the same shape Settle
		// builds for itself — by the time the terminal above landed, the Set
		// that led to it was published seconds ago, but confidence
		// enrichment and the RunID join are a nice-to-have on Result, not a
		// reason a whole harvest run should hang forever if this particular
		// lookup never resolves.
		setCtx, setCancel := context.WithTimeout(ctx, sc.SettleWindow)
		if s, ok := firstSetFor(setCtx, h.legs.Sets, sc.SignalRef); ok {
			res.RunID = s.RunID
			if len(s.Proposals) > 0 {
				top := s.Proposals[0]
				res.EmittedConfidence = top.Confidence
				res.ComputedConfidence = top.ComputedConfidence
				res.CeilingBound = top.ConfidenceCeilingBound
			}
		}
		setCancel()
	}

	res.ObservedSeverity = term.ObservedSeverity

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
	ExpectedVerdict    string                `json:"expectedVerdict" yaml:"expectedVerdict"` // approved, held, declined, or refused
	ActualVerdict      string                `json:"actualVerdict" yaml:"actualVerdict"`     // Terminal.Verdict — empty only when Err is set
	ActualContract     string                `json:"actualContract" yaml:"actualContract"`
	ActualResult       outcome.Result        `json:"actualResult" yaml:"actualResult"`
	EmittedConfidence  float64               `json:"emittedConfidence" yaml:"emittedConfidence"`
	ComputedConfidence float64               `json:"computedConfidence"`
	CeilingBound       bool                  `json:"ceilingBound" yaml:"ceilingBound"`
	ObservedSeverity   *float64              `json:"observedSeverity,omitempty" yaml:"observedSeverity,omitempty"` // the convergence watcher's measured end state — never a proxy for EmittedConfidence
	RunID              string                `json:"runID,omitempty" yaml:"runID,omitempty"`                       // joins to `task dev:transcript RUN=<id>`; empty on a refused row — nothing was ever proposed
	RunIndex           int                   `json:"runIndex" yaml:"runIndex"`                                     // which repeat pass produced this row
	StartedAt          time.Time             `json:"startedAt" yaml:"startedAt"`
	EndedAt            time.Time             `json:"endedAt" yaml:"endedAt"`
	// Err is the rendered error text, not the error interface itself:
	// encoding/json refuses to unmarshal any non-null value into an
	// interface-typed field other than interface{}, so a bare `error` field
	// here would let harvest --json write a Result that harvest --json can
	// never read back — exactly the read scorecard exists to do.
	Err string `json:"err,omitempty" yaml:"err,omitempty"`
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
	serverName := fs.String("server-name", "", "TLS SAN to verify the peer against, if it differs from the dialed host (e.g. a port-forwarded nats-url)")
	only := fs.String("row", "", "run only the scenario whose name contains this substring")
	asJSON := fs.Bool("json", false, "print each Result as JSON instead of a human line")
	kubeContext := fs.String("kube-context", "", "expected kubectl context — refuses to fire any scenario unless the active context matches; strongly recommended, omit only to fire without the check")
	repeat := fs.Int("repeat", 1, "number of passes over the filtered table, round-robin across rows")
	cooldown := fs.Duration("cooldown", defaultCooldown, "sleep after each row's restore, before the next fires — must outlive rattle's trailing burn window and clank's dedupe window")
	refusalGrace := fs.Duration("refusal-grace", defaultRefusalGrace, "once a detection for a row's signalRef is seen, how long to wait for a proposal.Set before calling the row refused")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *scenariosPath == "" || *natsURL == "" {
		_, _ = fmt.Fprintln(stderr, "usage: harvest --scenarios <path> --nats-url <url> [--tls-cert path --tls-key path --tls-ca path --server-name name] [--row substring] [--json] [--kube-context name] [--repeat N] [--cooldown duration] [--refusal-grace duration]")
		return 2
	}
	if *repeat < 1 {
		_, _ = fmt.Fprintln(stderr, "harvest: --repeat must be at least 1")
		return 2
	}

	table, err := LoadScenarios(*scenariosPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "harvest:", err)
		return 1
	}

	if *kubeContext != "" {
		if err := verifyKubeContext(context.Background(), *kubeContext, kubectlCurrentContext); err != nil {
			_, _ = fmt.Fprintln(stderr, "harvest:", err)
			return 1
		}
	} else {
		_, _ = fmt.Fprintln(stderr, "harvest: no --kube-context given — firing against whatever kubectl context is currently active")
	}

	// A harvest most often ends with a human losing patience, not the table
	// running out — Ctrl-C must reach ctx so Run's deferred restore (which
	// deliberately detaches via context.WithoutCancel) still fires instead
	// of leaving the fault and every precondition applied on the rig.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tc := tlsx.Config{CertFile: *certFile, KeyFile: *keyFile, CAFile: *caFile, ServerName: *serverName}
	js, closer, err := broker.Connect(ctx, *natsURL, tc, broker.Hooks{})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "harvest:", err)
		return 1
	}
	defer closer()

	legs := Legs{
		Outcomes:   NewNATSWatcher(js),
		Declines:   NewNATSDeclineWatcher(js),
		Held:       NewNATSHeldWatcher(js),
		Detections: NewNATSDetectionWatcher(js),
		Sets:       NewNATSSetWatcher(js),
	}
	h := NewHarvest(legs, CommandRunner{}, *refusalGrace)
	return run(ctx, h, table, *only, *asJSON, *repeat, *cooldown, stdout, stderr)
}

// run fires every scenario in table whose name contains only (all of them if
// only is empty) against h, repeat times round-robin across rows, printing
// one Result per run — split out from Main so a cancelled ctx's effect on
// restore is assertable without a live NATS connection or an actual SIGTERM.
// Returns 1 if any scenario's own execution failed, 0 otherwise.
func run(ctx context.Context, h *Harvest, table Table, only string, asJSON bool, repeat int, cooldown time.Duration, stdout, stderr io.Writer) int {
	rows := make([]Scenario, 0, len(table.Scenarios))
	for _, sc := range table.Scenarios {
		if only == "" || strings.Contains(sc.Name, only) {
			rows = append(rows, sc)
		}
	}

	failed := false
	total := repeat * len(rows)
	n := 0
	for i := 0; i < repeat; i++ {
		for _, sc := range rows {
			if ctx.Err() != nil {
				return exitCode(failed)
			}
			res, runErr := h.Run(ctx, sc)
			res.RunIndex = i
			n++
			if runErr != nil {
				failed = true
			}
			if asJSON {
				if err := json.NewEncoder(stdout).Encode(res); err != nil {
					_, _ = fmt.Fprintln(stderr, "harvest:", err)
					return 1
				}
			} else {
				printResult(stdout, res)
			}

			if n >= total {
				continue
			}
			select {
			case <-ctx.Done():
				return exitCode(failed)
			case <-time.After(cooldown):
			}
		}
	}

	return exitCode(failed)
}

func exitCode(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

func printResult(w io.Writer, res Result) {
	status := "OK"
	if res.Err != "" {
		status = "ERR"
	}
	_, _ = fmt.Fprintf(w, "%-4s %-30s run=%-2d expected=%s/%s/%s actual=%s/%s/%s runID=%s confidence=%.3f computed=%.3f ceiling=%t",
		status, res.ScenarioName, res.RunIndex, res.ExpectedClass, res.ExpectedContract, res.ExpectedVerdict,
		res.ActualContract, res.ActualVerdict, res.ActualResult, res.RunID,
		res.EmittedConfidence, res.ComputedConfidence, res.CeilingBound)
	if res.Err != "" {
		_, _ = fmt.Fprintf(w, " err=%q", res.Err)
	}
	_, _ = fmt.Fprintln(w)
}
