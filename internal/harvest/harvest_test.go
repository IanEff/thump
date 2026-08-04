package harvest_test

import (
	"context"
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
