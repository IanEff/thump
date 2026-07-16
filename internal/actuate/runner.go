package actuate

import (
	"context"
	"fmt"
	"os/exec"
)

// CommandRunner is the **impure** seam: run an external command; return
// its combined output.  Injected so tests pass faxes and never touch a
// cluater.  Production defaults to execRunner.

type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec
}

// binding is a ref's forward argv and und argv.
type binding struct {
	forward []string
	reverse []string
}

// Runner satisfies thump.ActionRunner.  It maps a contract ref to a
// real kubectl invocation and runs it through the injected CommandRunner.
type Runner struct {
	run      CommandRunner
	bindings map[string]binding
}

// New returns a production Runner that actually shells out.
func New() *Runner {
	return newWith(execRunner)
}

// newWith is the test seam: inject a fake commandRunner to assert argc without a ckuster.  Kept unexported.
func newWith(run CommandRunner) *Runner {
	return &Runner{
		run: run,
		bindings: map[string]binding{
			"hold-rebalance": {
				forward: []string{"kubectl", "-n", "rook-ceph", "exec", "deploy/rook-ceph-tools", "--", "ceph", "osd", "set", "noout"},
				reverse: []string{"kubectl", "-n", "rook-ceph", "exec", "deploy/rook-ceph-tools", "--", "ceph", "osd", "unset", "noout"},
			},
			"scale-out-rgw-gateways": {
				// ⚠️ instances hardcoded 2→1 for v1. Computing the target from
				// params.additional_replicas needs the CURRENT count first, which
				// means a read before the write — deliberately deferred (Part 7).
				forward: []string{"kubectl", "-n", "rook-ceph", "patch", "cephobjectstore", "ceph-objectstore", "--type", "merge", "-p", `{"spec":{"gateway":{"instances":2}}}`},
				reverse: []string{"kubectl", "-n", "rook-ceph", "patch", "cephobjectstore", "ceph-objectstore", "--type", "merge", "-p", `{"spec":{"gateway":{"instances":1}}}`},
			},
		},
	}
}

// Run dispatches ref's forward (or reverse) command.  An unbound ref is an error.
func (r *Runner) Run(ctx context.Context, ref string, reverse bool, _ map[string]float64) error {
	b, ok := r.bindings[ref]
	if !ok {
		return fmt.Errorf("actuate: ref %q is not bound to a command", ref)
	}
	argv := b.forward
	if reverse {
		argv = b.reverse
	}
	out, err := r.run(ctx, argv[0], argv[1:]...)
	if err != nil {
		return fmt.Errorf("actuate: %s %v: %w: %s", ref, argv, err, out)
	}
	return nil
}
