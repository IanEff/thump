package harvest_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/harvest"
)

// recordingRunner records every command it was asked to run, so a test can
// assert restore ran even when the run that triggered it was cancelled.
type recordingRunner struct {
	mu    sync.Mutex
	calls []string
}

// Run honours the context it was handed, which is the whole reason this
// double exists: a runner that ignores cancellation records the restore
// commands whether or not restore detached from the cancelled context, so
// the WithoutCancel claim would pass with WithoutCancel deleted.
func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

func (blockingWatcher) Outcomes(_ context.Context) (<-chan outcome.Outcome, error) {
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
			{
				Name:    "mon-osd-down-out-interval",
				Set:     "ceph config set global mon_osd_down_out_interval 60",
				Restore: "ceph config set global mon_osd_down_out_interval 600",
			},
			{
				Name: "balancer-off",
				Set:  "ceph balancer off", Restore: "ceph balancer on",
			},
		},
		Expects:      harvest.Expects{Verdict: "held"},
		SettleWindow: time.Minute,
		Restore: harvest.Action{
			Path: "chaos/osd-pod-failure-accelerate.yaml", Apply: "kubectl-delete",
		},
	}

	runner := &recordingRunner{}
	legs := harvest.Legs{Outcomes: blockingWatcher{}, Sets: feedSetWatcher(nil)}
	h := harvest.NewHarvest(legs, runner, 0, nil)

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

// TestRun_RestoresThePreconditionsWhenTheHarvestIsCancelledMidFlight pins
// the property a killed harvest depends on: Main hands run the same ctx it
// built from signal.NotifyContext, so an operator's Ctrl-C reaches Run's
// deferred restore (which runs under context.WithoutCancel, never the
// cancelled ctx directly) instead of leaving the fault and every
// precondition applied on the rig. Unlike
// TestHarvest_RestoresEveryPreconditionWhenTheRunIsCancelledMidFlight above,
// this drives the exported run wiring Main calls, not Harvest.Run directly —
// closing the gap where nothing exercised Main's own ctx construction.
func TestRun_RestoresThePreconditionsWhenTheHarvestIsCancelledMidFlight(t *testing.T) {
	t.Parallel()
	sc := harvest.Scenario{
		Name: "osd-down-accelerate",
		Fault: harvest.Action{
			Path: "chaos/osd-pod-failure-accelerate.yaml", Apply: "kubectl",
		},
		Preconditions: []harvest.Precondition{
			{
				Name:    "mon-osd-down-out-interval",
				Set:     "ceph config set global mon_osd_down_out_interval 60",
				Restore: "ceph config set global mon_osd_down_out_interval 600",
			},
			{
				Name: "balancer-off",
				Set:  "ceph balancer off", Restore: "ceph balancer on",
			},
		},
		Expects:      harvest.Expects{Verdict: "held"},
		SettleWindow: time.Minute,
		Restore: harvest.Action{
			Path: "chaos/osd-pod-failure-accelerate.yaml", Apply: "kubectl-delete",
		},
	}

	runner := &recordingRunner{}
	legs := harvest.Legs{Outcomes: blockingWatcher{}, Sets: feedSetWatcher(nil)}
	h := harvest.NewHarvest(legs, runner, 0, nil)
	table := harvest.Table{Scenarios: []harvest.Scenario{sc}}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		harvest.RunForTest(ctx, h, table, "", false, 1, 0, io.Discard, io.Discard)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunForTest did not return after cancellation")
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

// TestHarvest_RunFailsLoudlyOnADeadBrokerBeforeTheFaultEverFires pins that a
// liveness failure produces ErrBrokerUnreachable rather than the
// settle-timeout shape a dead tunnel used to produce, and never applies the
// fault — a dead broker means the watch leg was always doomed, so injecting
// the fault would only strand the rig.
func TestHarvest_RunFailsLoudlyOnADeadBrokerBeforeTheFaultEverFires(t *testing.T) {
	t.Parallel()
	sc := harvest.Scenario{
		Name:         "dead-tunnel",
		Fault:        harvest.Action{Path: "chaos/x.yaml", Apply: "kubectl"},
		Restore:      harvest.Action{Path: "chaos/x.yaml", Apply: "kubectl-delete"},
		SettleWindow: time.Minute,
	}

	runner := &recordingRunner{}
	live := func(context.Context) error { return errors.New("dial timeout") }
	h := harvest.NewHarvest(harvest.Legs{}, runner, 0, live)

	_, err := h.Run(t.Context(), sc)
	if !errors.Is(err, harvest.ErrBrokerUnreachable) {
		t.Error("want ErrBrokerUnreachable", err)
	}
	if containsCall(runner.snapshot(), "kubectl apply -f chaos/x.yaml") {
		t.Error("Run applied the fault despite a failed liveness check")
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

// failingRunner records every command like recordingRunner, but errors on
// any call containing failOn — used to force a precondition to fail partway
// through Run.
type failingRunner struct {
	mu     sync.Mutex
	calls  []string
	failOn string
}

func (r *failingRunner) Run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	call := name + " " + joinArgs(args)
	r.calls = append(r.calls, call)
	if strings.Contains(call, r.failOn) {
		return errors.New("boom")
	}
	return nil
}

func (r *failingRunner) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// TestHarvest_RestoresAlreadyAppliedPreconditionsWhenALaterOnePartwayFails
// pins that a precondition failing partway through Run doesn't strand the
// rig: every precondition that already applied has its restore command run,
// in reverse order, and the fault itself is never applied.
func TestHarvest_RestoresAlreadyAppliedPreconditionsWhenALaterOnePartwayFails(t *testing.T) {
	t.Parallel()
	sc := harvest.Scenario{
		Name: "osd-down-accelerate",
		Fault: harvest.Action{
			Path: "chaos/osd-pod-failure-accelerate.yaml", Apply: "kubectl",
		},
		Preconditions: []harvest.Precondition{
			{
				Name:    "mon-osd-down-out-interval",
				Set:     "ceph config set global mon_osd_down_out_interval 60",
				Restore: "ceph config set global mon_osd_down_out_interval 600",
			},
			{
				Name: "argocd-disable",
				Set:  "argocd app set --sync-policy manual", Restore: "argocd app set --sync-policy automated",
			},
		},
		Expects:      harvest.Expects{Verdict: "held"},
		SettleWindow: time.Minute,
		Restore: harvest.Action{
			Path: "chaos/osd-pod-failure-accelerate.yaml", Apply: "kubectl-delete",
		},
	}

	runner := &failingRunner{failOn: "argocd"}
	legs := harvest.Legs{Outcomes: blockingWatcher{}, Sets: feedSetWatcher(nil)}
	h := harvest.NewHarvest(legs, runner, 0, nil)

	if _, err := h.Run(t.Context(), sc); err == nil {
		t.Fatal("Run succeeded despite a failing precondition")
	}

	calls := runner.snapshot()
	want := "/bin/sh -c ceph config set global mon_osd_down_out_interval 600"
	if !containsCall(calls, want) {
		t.Errorf("Run left mon_osd_down_out_interval at 60 after a later precondition failed; restore calls were %v", calls)
	}
}

// TestHarvest_PopulatesConfidenceFromTheMatchingProposalSet pins that a
// settled row's confidence fields come from the Set Settle itself observed
// for the row's own signalRef, not a fabricated or stale value.
func TestHarvest_PopulatesConfidenceFromTheMatchingProposalSet(t *testing.T) {
	t.Parallel()
	const fp = "slo_burn:ceph-cluster"

	sc := harvest.Scenario{
		Name:         "confidence-enrichment",
		SignalRef:    fp,
		Fault:        harvest.Action{Path: "noop", Apply: "exec"},
		Restore:      harvest.Action{Path: "noop", Apply: "exec"},
		SettleWindow: 5 * time.Second,
	}

	set := proposal.Set{
		SignalRef: fp,
		Proposals: []proposal.Candidate{
			{ContractRef: "restart-pod", Confidence: 0.7, ComputedConfidence: 0.65, ConfidenceCeilingBound: true},
		},
	}
	fixture := newOrderedSetThenTerminal(set)
	fixture.outcomes = []outcome.Outcome{{SignalRef: fp, Result: outcome.ResultSuccess}}
	legs := harvest.Legs{Outcomes: fixture, Sets: fixture}
	h := harvest.NewHarvest(legs, &recordingRunner{}, 0, nil)

	res, err := h.Run(t.Context(), sc)
	if err != nil {
		t.Fatal(err)
	}
	// StartedAt/EndedAt are wall-clock timestamps Run stamps itself — not
	// part of the confidence-enrichment claim this test pins.
	res.StartedAt, res.EndedAt = time.Time{}, time.Time{}

	want := harvest.Result{
		ScenarioName:       sc.Name,
		ActualVerdict:      "approved",
		ActualResult:       outcome.ResultSuccess,
		EmittedConfidence:  0.7,
		ComputedConfidence: 0.65,
		CeilingBound:       true,
	}
	if diff := cmp.Diff(want, res); diff != "" {
		t.Error("wrong Result after a successful settle (-want +got)", diff)
	}
}
