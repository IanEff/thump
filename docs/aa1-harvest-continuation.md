# AA1 continuation — the harvest barrier

Working notes to get `internal/harvest` from its current stub state to
"AA1 done when" in `phase-aa-implementation-guide.md`. Written offline,
2026-08-04. **Nothing in `internal/harvest` or `chaos/` has been touched by
me** — this is scratch code for you to paste in, not a diff that landed.

## Are you off track? No.

Here's what's actually on disk right now, and it's further along than the
"miles off" feeling suggests:

- `chaos/scenarios.yaml` — **done**, matches the guide's Step 2 exactly.
  Nothing to do here.
- `internal/harvest/scenario_test.go` — the guard test from Step 1, but it
  has two bugs that would fail to compile once `scenario.go` exists (see
  below). Neither is a design problem, both are quick fixes.
- `internal/harvest/settle_test.go` — the real Step 3 tests, verbatim from
  the guide. They reference `feedWatcher` and `silentWatcher`, which don't
  exist yet — that's expected, they're test doubles that live beside the
  test, not in the guide's prose.
- `internal/harvest/settle.go` — a compiling skeleton (`Settle` always
  returns `outcome.Outcome{}, nil`). This is exactly the right shape to be
  in mid-TDD: signature pinned, body not yet written, tests currently red
  for the right reason.
- `internal/harvest/harvest.go` — same story: `Harvest`, `Result`, and
  method *signatures* are pinned (`func (h *Harvest) Run(...) (Result, error)`
  with no body — that's not valid Go as a real function, more on that
  below), but there's no `Scenario` type yet for it to reference, no
  `CommandRunner`, and no test file.

So the honest scorecard: **Step 2 done, Step 1 half-wired, Step 3 signatures
right/body missing, Step 4/5 not started.** That's a normal place to be
partway through a guide — nothing here suggests a wrong turn.

One thing to fix immediately: `harvest.go:17-19` has function *declarations*
with no body:

```go
func (h *Harvest) Run(ctx context.Context, sc Scenario) (Result, error)
func (h *Harvest) restore(ctx context.Context, sc Scenario) error
```

That's not legal Go for a function with a receiver defined in a normal
source file — it compiles only as an external/assembly-linked declaration,
which isn't what's happening here. This is presumably a placeholder you
typed to pin the signature before filling the body (a totally reasonable
TDD move — signature first, body via `panic("todo")` next). Go won't build
the package until each either gets a body or gets deleted. The code below
gives both bodies.

---

## What to reuse from around the repo (Ian's instinct was right)

Nothing in `thump`/`clank`/`rattle`/`hiss` shells out to a subprocess today
— there is no `exec.Command` anywhere in `internal/`. So `CommandRunner`
itself is genuinely new; there's no existing actuator to lift it from. But
three other things **do** exist and should be reused rather than
reinvented:

1. **`internal/configfile`** (`Stage[F]`, `Require`) — the same staging
   pattern `clank.LoadWeightsFile` uses: a pointer-typed staging struct so
   YAML's zero-value collapse can't hide an omitted key, `sigs.k8s.io/yaml`
   under the hood (JSON tags, not YAML tags — that's why every struct below
   uses `json:"..."` even though the file is `.yaml`). `LoadScenarios`
   below uses `configfile.Stage` for the read+parse step.

2. **`internal/contract.StaticCatalog`** — the guide's own prose says
   `cat.Lookup` "may not be the accessor `StaticCatalog` exposes." It
   isn't. The real method is `ByName(name string) (ActionContract, bool)`
   (`internal/contract/contract.go:70`). Fix the test to call that.

3. **`api/v1/outcome.Result`** — the terminal/non-terminal split `Settle`
   needs is the same rule `clank.CollapseCases` already encodes
   (`internal/clank/corpus.go:107`, skips `ResultApplied`). Don't
   re-derive the enum; mirror the one-line rule.

---

## Fix 1 — `scenario_test.go`

Two bugs, both compile-time:

```diff
 package harvest_test

 import (
 	"os"
 	"path/filepath"
 	"testing"

 	"github.com/ianeff/thump/internal/contract"
+	"github.com/ianeff/thump/internal/harvest"
 )
```

and:

```diff
-			if _, ok := cat.Lookup(sc.Expects.ContractRef); !ok {
+			if _, ok := cat.ByName(sc.Expects.ContractRef); !ok {
```

That's the whole fix — the test's logic and structure are already right.

---

## `internal/harvest/scenario.go` (new)

```go
// Package harvest drives a chaos scenario end to end: preflight,
// preconditions, fault, settle, restore, graded result. It is rig-aware but
// never rig-mutating on its own initiative — every mutation a Scenario
// makes is named in the scenario table, and every one has a restore.
package harvest

import (
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/configfile"
)

// ErrInvalidScenario means a row in the scenario table failed validation.
// LoadScenarios refuses the whole table rather than skip the bad row — a
// harvest run silently short one scenario is a miscount nobody notices
// until the corpus looks thin.
var ErrInvalidScenario = errors.New("harvest: scenario table is invalid")

// Scenario is one calibration datum's worth of work: what to break, what
// has to be true first, what the engine is expected to conclude, how long
// to let it settle, and what puts the rig back.
type Scenario struct {
	Name          string
	Domain        string
	Fault         Action
	Preconditions []Precondition
	Expects       Expects
	SettleWindow  time.Duration
	Restore       Action
}

// Action is one thing the harvest does to the cluster: apply a fault
// manifest, exec a script, or delete/reverse what a prior Action applied.
// Apply is one of "kubectl", "kubectl-delete", "exec" — LoadScenarios
// refuses anything else at load rather than let CommandRunner discover an
// unknown verb mid-run.
type Action struct {
	Path  string
	Apply string
	Args  []string
}

// Precondition is set before the fault fires and its Restore run after, in
// reverse declaration order — mon_osd_down_out_interval and the balancer
// are the two the 2026-08-02 session found by hand and this table now
// carries as data instead of a running note.
type Precondition struct {
	Name    string
	Set     string
	Restore string
}

// Expects is what the settled outcome is graded against. Verdict is one of
// "approved", "held", "declined" — hiss's governance vocabulary, not a
// harvest invention.
type Expects struct {
	FailureClass proposal.FailureClass
	ContractRef  string
	Verdict      string
}

var validApply = map[string]bool{
	"kubectl":        true,
	"kubectl-delete": true,
	"exec":           true,
}

var validVerdict = map[string]bool{
	"approved": true,
	"held":     true,
	"declined": true,
}

// --- staging shape: mirrors chaos/scenarios.yaml's keys one-for-one ---

type scenariosFile struct {
	Version   int             `json:"version"`
	Scenarios []scenarioStage `json:"scenarios"`
}

type scenarioStage struct {
	Name          string              `json:"name"`
	Domain        string              `json:"domain"`
	Fault         actionStage         `json:"fault"`
	Preconditions []preconditionStage `json:"preconditions"`
	Expects       expectsStage        `json:"expects"`
	SettleWindow  string              `json:"settleWindow"`
	Restore       actionStage         `json:"restore"`
}

type actionStage struct {
	Path  string   `json:"path"`
	Apply string   `json:"apply"`
	Args  []string `json:"args"`
}

type preconditionStage struct {
	Name    string `json:"name"`
	Set     string `json:"set"`
	Restore string `json:"restore"`
}

type expectsStage struct {
	FailureClass string `json:"failureClass"`
	ContractRef  string `json:"contractRef"`
	Verdict      string `json:"verdict"`
}

// LoadScenarios reads path and validates it into a []Scenario. It fails at
// load with every fault named, never at first use with a zero value that
// reads as real data — the same posture LoadWeightsFile takes
// (internal/clank/weights.go).
func LoadScenarios(path string) ([]Scenario, error) {
	sf, err := configfile.Stage[scenariosFile](path, "scenario table")
	if err != nil {
		return nil, err
	}

	out := make([]Scenario, 0, len(sf.Scenarios))
	for _, s := range sf.Scenarios {
		sc, err := s.validate()
		if err != nil {
			return nil, fmt.Errorf("%w: scenario %q: %w", ErrInvalidScenario, s.Name, err)
		}
		out = append(out, sc)
	}
	return out, nil
}

func (s scenarioStage) validate() (Scenario, error) {
	if s.Name == "" {
		return Scenario{}, errors.New("scenario has no name")
	}
	fault, err := s.Fault.validate()
	if err != nil {
		return Scenario{}, fmt.Errorf("fault: %w", err)
	}
	restore, err := s.Restore.validate()
	if err != nil {
		return Scenario{}, fmt.Errorf("restore: %w", err)
	}
	if restore.Path == "" {
		return Scenario{}, errors.New("no restore — a harvest that cannot restore is a rig teardown")
	}
	if !validVerdict[s.Expects.Verdict] {
		return Scenario{}, fmt.Errorf("expects.verdict %q not one of approved, held, declined", s.Expects.Verdict)
	}
	if s.Expects.ContractRef == "" {
		return Scenario{}, errors.New("expects.contractRef is empty")
	}
	window, err := time.ParseDuration(s.SettleWindow)
	if err != nil {
		return Scenario{}, fmt.Errorf("settleWindow %q: %w", s.SettleWindow, err)
	}
	if window <= 0 {
		return Scenario{}, fmt.Errorf("settleWindow %q must be positive", s.SettleWindow)
	}

	preconditions := make([]Precondition, 0, len(s.Preconditions))
	for _, p := range s.Preconditions {
		if p.Set == "" || p.Restore == "" {
			return Scenario{}, fmt.Errorf("precondition %q must have both set and restore", p.Name)
		}
		preconditions = append(preconditions, Precondition{
			Name: p.Name, Set: p.Set, Restore: p.Restore,
		})
	}

	return Scenario{
		Name:          s.Name,
		Domain:        s.Domain,
		Fault:         fault,
		Preconditions: preconditions,
		Expects: Expects{
			FailureClass: proposal.FailureClass(s.Expects.FailureClass),
			ContractRef:  s.Expects.ContractRef,
			Verdict:      s.Expects.Verdict,
		},
		SettleWindow: window,
		Restore:      restore,
	}, nil
}

func (a actionStage) validate() (Action, error) {
	if a.Path == "" {
		return Action{}, errors.New("path is empty")
	}
	if !validApply[a.Apply] {
		return Action{}, fmt.Errorf("apply %q not one of kubectl, kubectl-delete, exec", a.Apply)
	}
	return Action{Path: a.Path, Apply: a.Apply, Args: a.Args}, nil
}
```

That satisfies `scenario_test.go` as written: it reads `chaos/scenarios.yaml`
for real, resolves `contract.ByName` against `config/actions/catalog.yaml`,
and both scenario rows already name real actions and real fault files, so
the guard goes **green** once this file exists.

**Hand-verify it red**, per the guide's discipline: temporarily typo
`contractRef: accelerate-recover` (drop the last `y`) in `chaos/scenarios.yaml`,
confirm the test fails naming that exact string, put the `y` back.

---

## `internal/harvest/settle.go` (fill in the body)

```go
package harvest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
)

// ErrSettleTimeout means the settle window elapsed with no terminal outcome
// on the fingerprint. It is a result worth recording, not a harness
// failure: an incident that never settles is the shape defence 4 exists to
// represent, and a run that reports it is more useful than one that waits
// forever.
var ErrSettleTimeout = errors.New("harvest: settle window elapsed with no terminal outcome")

// Watcher is how a harvest learns an incident finished. Production
// satisfies it by consuming thump.outcomes; tests satisfy it with a
// channel.
type Watcher interface {
	Outcomes(ctx context.Context) (<-chan outcome.Outcome, error)
}

// isTerminal mirrors clank.CollapseCases' rule (internal/clank/corpus.go),
// stated once more at the one other place a human is tempted to stop
// watching early: ResultApplied is the executor's immediate ack, superseded
// by whatever the convergence watcher settles. Every other Result is
// terminal.
func isTerminal(r outcome.Result) bool {
	return r != outcome.ResultApplied
}

// Settle blocks until w reports a terminal outcome for signalRef, or window
// elapses.
func Settle(ctx context.Context, w Watcher, signalRef string, window time.Duration) (outcome.Outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	ch, err := w.Outcomes(ctx)
	if err != nil {
		return outcome.Outcome{}, fmt.Errorf("harvest: watch %s: %w", signalRef, err)
	}

	for {
		select {
		case <-ctx.Done():
			return outcome.Outcome{}, fmt.Errorf("%w: %s", ErrSettleTimeout, signalRef)
		case o, ok := <-ch:
			if !ok {
				return outcome.Outcome{}, fmt.Errorf("%w: %s", ErrSettleTimeout, signalRef)
			}
			if o.SignalRef != signalRef || !isTerminal(o.Result) {
				continue
			}
			return o, nil
		}
	}
}
```

`settle_test.go` references `feedWatcher` and `silentWatcher` but neither
is defined anywhere in `internal/harvest`. Add them as test doubles —
they're small enough not to need their own file, but a `helpers_test.go`
keeps `settle_test.go` matching the guide byte-for-byte:

```go
// internal/harvest/helpers_test.go
package harvest_test

import (
	"context"

	"github.com/ianeff/thump/api/v1/outcome"
)

// feedWatcher replays a fixed sequence of Results on one fingerprint, then
// closes — a scripted convergence watcher for tests that don't need a real
// one.
type feedWatcher []outcome.Result

func (f feedWatcher) Outcomes(context.Context) (<-chan outcome.Outcome, error) {
	ch := make(chan outcome.Outcome, len(f))
	for _, r := range f {
		ch <- outcome.Outcome{SignalRef: "slo_burn:ceph-cluster", Result: r}
	}
	close(ch)
	return ch, nil
}

// silentWatcher never emits — the incident that never settles.
type silentWatcher struct{}

func (silentWatcher) Outcomes(context.Context) (<-chan outcome.Outcome, error) {
	return make(chan outcome.Outcome), nil
}
```

Run `go test ./internal/harvest/... -run TestSettle -v`. Both cases should
go green, and the timeout test should return near-instantly even though it
asks for a 20-minute window — that's `testing/synctest` faking the clock,
not a real wait.

---

## `internal/harvest/harvest.go` (fill in the bodies)

This is where `CommandRunner` gets designed — genuinely new, no existing
element in the repo to lift. Kept deliberately narrow: it knows how to run
one of three verbs (`kubectl apply`, `kubectl delete`, `exec` a chaos
script), and how to run a raw shell command for a precondition's `set`/
`restore` string (`ceph config set global ...`). Everything else about
"what to run" lives in the `Scenario`, not in the runner.

```go
package harvest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

// Runner executes one shell-level step of a harvest: applying or deleting a
// fault manifest, exec'ing a chaos script, or running a raw precondition
// command. It is an interface so Harvest's tests never actually shell out —
// the cancellation/restore test below fakes it.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

// CommandRunner is the production Runner: os/exec, one process per call, no
// shell interpretation beyond what exec.Command itself does. It does not
// retry and does not swallow a non-zero exit — a fault that failed to apply
// must not be graded as though it fired.
type CommandRunner struct{}

func (CommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: name/args are operator-authored scenario table entries, not user input
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, out)
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

const restoreTimeout = 2 * time.Minute

// Run fires one scenario end to end: preconditions, fault, settle, restore,
// graded result. It never mines — mining is calipers' own verb
// (internal/corpus/mine.go), and a harvest that also wrote the corpus would
// make a failed restore and a polluted record the same incident.
func (h *Harvest) Run(ctx context.Context, sc Scenario) (Result, error) {
	res := Result{
		ScenarioName:     sc.Name,
		ExpectedClass:    sc.Expects.FailureClass,
		ExpectedContract: sc.Expects.ContractRef,
		ExpectedVerdict:  sc.Expects.Verdict,
	}

	// Preconditions run in declared order; restore always runs, even on a
	// failure partway through the rest of Run — that's what the deferred
	// h.restore below is for.
	for _, p := range sc.Preconditions {
		if err := h.runAction(ctx, p.Set); err != nil {
			res.Err = fmt.Errorf("precondition %s: %w", p.Name, err)
			return res, res.Err
		}
	}
	defer func() {
		if err := h.restore(ctx, sc); err != nil {
			// A failed restore is the session's most important finding, not
			// a log line to lose: surface it even though Run already
			// returned a result.
			res.Err = fmt.Errorf("%w (restore also failed: %v)", res.Err, err)
		}
	}()

	if err := h.applyAction(ctx, sc.Fault); err != nil {
		res.Err = fmt.Errorf("fault: %w", err)
		return res, res.Err
	}

	fingerprint := sc.Domain + ":" + sc.Name // placeholder join key; real
	// wiring threads the SignalRef the detector actually assigned once
	// AA1 runs against a live rattle/clank pair — see "one live session".
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

// restore puts the rig back: the fault's own reversal, then every
// precondition's, in reverse order. It deliberately does not inherit the
// caller's cancellation — a harvest is most often stopped by a human losing
// patience, and a restore that inherits that cancellation reverts nothing
// while reporting nothing. The deadline is authored here because a restore
// that hangs on an unreachable cluster is worse than one that fails loudly.
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

// applyAction dispatches an Action by its Apply verb.
func (h *Harvest) applyAction(ctx context.Context, a Action) error {
	switch a.Apply {
	case "kubectl":
		return h.runner.Run(ctx, "kubectl", append([]string{"apply", "-f", a.Path}, a.Args...)...)
	case "kubectl-delete":
		return h.runner.Run(ctx, "kubectl", append([]string{"delete", "-f", a.Path}, a.Args...)...)
	case "exec":
		return h.runner.Run(ctx, a.Path, a.Args...)
	default:
		// LoadScenarios already refuses this at load time; reaching here
		// means a Scenario was built by hand rather than through the
		// loader, which a test is allowed to do but production never will.
		return fmt.Errorf("harvest: unknown apply verb %q", a.Apply)
	}
}

// runAction runs a raw precondition command string ("ceph config set
// global ...") through /bin/sh -c, the one place this package accepts a
// whole command line rather than a program and argv — preconditions are
// authored in the scenario table by the same person who can edit
// chaos/*.sh, so the trust boundary is the same as the fault files
// themselves.
func (h *Harvest) runAction(ctx context.Context, command string) error {
	return h.runner.Run(ctx, "/bin/sh", "-c", command)
}

type Result struct {
	ScenarioName      string                `json:"scenarioName" yaml:"scenarioName"`
	ExpectedClass     proposal.FailureClass `json:"expectedClass" yaml:"expectedClass"`
	ExpectedContract  string                `json:"expectedContract" yaml:"expectedContract"`
	ExpectedVerdict   string                `json:"expectedVerdict" yaml:"expectedVerdict"`
	ActualResult      outcome.Result        `json:"actualResult" yaml:"actualResult"`
	EmittedConfidence float64               `json:"emittedConfidence" yaml:"emittedConfidence"`
	CeilingBound      bool                  `json:"ceilingBound" yaml:"ceilingBound"`
	Err               error                 `json:"err" yaml:"err"`
}
```

Add the `"errors"` and `"time"` imports at the top alongside the rest.

**One thing flagged, not resolved:** `fingerprint := sc.Domain + ":" + sc.Name` is a placeholder. The guide doesn't say how `Run` learns the real `SignalRef` `Settle` should watch for — that only exists once rattle has actually detected the injected fault and clank has fingerprinted it, which is exactly the live-session step 2 ("does the settle wait return on the settled outcome"). Don't treat the placeholder as done; it's a seam to revisit once you're driving this against a real cluster, not before.

---

## `internal/harvest/harvest_test.go` (new)

The guide's Step 4 test, filled in with a fake `Runner` and `Watcher` so it
proves the restore-survives-cancellation property without touching a
cluster:

```go
package harvest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/harvest"
)

// recordingRunner records every command it was asked to run, so a test can
// assert restore ran even when the run that triggered it was cancelled.
type recordingRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name+" "+joinArgs(args))
	return nil
}

func (r *recordingRunner) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

// blockingWatcher never emits, so Settle blocks until the test cancels ctx —
// standing in for a harvest a human stopped early.
type blockingWatcher struct{}

func (blockingWatcher) Outcomes(ctx context.Context) (<-chan outcome.Outcome, error) {
	ch := make(chan outcome.Outcome)
	return ch, nil
}

func TestHarvest_RestoresEveryPreconditionWhenTheRunIsCancelledMidFlight(t *testing.T) {
	t.Parallel()
	// preflight.sh already patches selfHeal:false onto six ArgoCD
	// Applications, so a harvest leaves the rig materially changed from the
	// first second — and the human most likely to stop it early is the one
	// who least wants to remember what to undo by hand.
	sc := harvest.Scenario{
		Name: "osd-down-accelerate",
		Fault: harvest.Action{
			Path: "chaos/osd-pod-failure-accelerate.yaml", Apply: "kubectl",
		},
		Preconditions: []harvest.Precondition{
			{Name: "mon-osd-down-out-interval",
				Set:     "ceph config set global mon_osd_down_out_interval 60",
				Restore: "ceph config set global mon_osd_down_out_interval 600"},
			{Name: "balancer-off",
				Set: "ceph balancer off", Restore: "ceph balancer on"},
		},
		Expects:      harvest.Expects{Verdict: "held"},
		SettleWindow: time.Minute,
		Restore: harvest.Action{
			Path: "chaos/osd-pod-failure-accelerate.yaml", Apply: "kubectl-delete",
		},
	}

	runner := &recordingRunner{}
	h := harvest.NewHarvest(blockingWatcher{}, runner)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_, _ = h.Run(ctx, sc)
		close(done)
	}()

	// Give Run time to reach Settle (preconditions + fault applied), then
	// simulate the human losing patience.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	calls := runner.snapshot()
	wantSuffix := []string{
		"kubectl delete -f chaos/osd-pod-failure-accelerate.yaml",
		"/bin/sh -c ceph balancer on",
		"/bin/sh -c ceph config set global mon_osd_down_out_interval 600",
	}
	for _, want := range wantSuffix {
		if !containsCall(calls, want) {
			t.Errorf("restore did not run %q; calls were %v", want, calls)
		}
	}
}

func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}
```

**Hand-verify this one red**, per the guide: temporarily delete
`context.WithoutCancel(ctx)` in `restore` (leave the bare `ctx`), re-run —
it should fail naming a missing restore call, because the restore's own
context died the instant `cancel()` fired. Put `WithoutCancel` back.

---

## Checklist against "AA1 done when"

- [ ] Committed `chaos/scenarios.yaml` passes the guard — **already true**,
      confirm after adding `scenario.go` and the two `scenario_test.go`
      fixes above.
- [ ] Guard hand-verified red by mistyping one `contractRef` — do this once,
      by hand, then revert.
- [ ] `Settle` returns on the settled outcome, not `applied`, and reports
      its own timeout — covered by `settle_test.go` once `helpers_test.go`
      exists.
- [ ] Cancelled-mid-flight restore test green and hand-verified red — the
      new `harvest_test.go` above, hand-verify via the `WithoutCancel`
      deletion.
- [ ] Every 2026-08-02 precondition is a row in the file — **already true**,
      both rows are in `chaos/scenarios.yaml`.
- [ ] `task ci` green, run **unpiped** (`task ci`, not `task ci | tail` —
      your own memory has a note about why: piped exit codes lie).

Once this is green, AA1 is closed and independent of AA2–AA4 — you can pick
up AA2 (`internal/rca`) next without waiting on anything here, per the
guide's sequencing diagram. The only place AA1 gets exercised again is the
one live session at the very end, after AA5.
