// Package actuate is thump's imperative shell: the one place that turns an
// authored contract ref into a real mutation against the cluster. It lives
// outside package thump on purpose — thump's structural_test.go forbids any
// cluster-client import in the reasoning/rendering path, so the ability to
// touch infrastructure is quarantined here, behind the thump.ActionRunner
// interface, and reachable only through the single allowlisted import.
//
// The package splits a functional core from an imperative shell: runner.go
// (this file) is client-go-free — it maps a ref to a typed operation and
// dispatches it through the Kube seam — and kube.go holds every client-go
// call. Tests drive the core with a fake Kube and never reach an apiserver.
package actuate

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// Kube is the impure seam actuate reaches the cluster through — exec a
// command inside a pod (selected by label), or merge-patch a named custom
// resource. Expressed in primitives, not client-go types, so the fake in
// tests is trivial and the pure core above stays free of any apiserver
// import. Production wires a liveKube (kube.go); tests inject a recorder.
type Kube interface {
	// Exec runs command inside the first Running pod matching selector in
	// namespace, returning an error (with captured stderr) if it fails. No
	// shell is involved — command is argv handed straight to the container.
	Exec(ctx context.Context, namespace, selector string, command []string) error
	// Patch applies a merge patch to the named resource identified by its
	// group/version/resource in namespace.
	Patch(ctx context.Context, group, version, resource, namespace, name string, mergePatch []byte) error
}

// operation is one ref-direction's concrete mutation — an exec into a pod or
// a patch of a resource. A binding pairs the forward op with its reverse so
// the undo is authored right next to the action it undoes.
type operation interface {
	do(ctx context.Context, k Kube) error
}

// execOp runs argv inside the pod matched by selector — the shape every ceph
// toolbox command takes (the mutation happens in the toolbox's exec
// environment, reached over the apiserver's exec subresource; thump's own
// distroless image needs no shell or ceph binary).
type execOp struct {
	namespace string
	selector  string
	command   []string
}

func (e execOp) do(ctx context.Context, k Kube) error {
	return k.Exec(ctx, e.namespace, e.selector, e.command)
}

// patchOp merge-patches one named custom resource — no exec, no toolbox.
type patchOp struct {
	group, version, resource string
	namespace, name          string
	patch                    []byte
}

func (p patchOp) do(ctx context.Context, k Kube) error {
	return k.Patch(ctx, p.group, p.version, p.resource, p.namespace, p.name, p.patch)
}

// binding is a ref's forward mutation and its authored undo.
type binding struct {
	forward operation
	reverse operation
}

// Runner satisfies thump.ActionRunner: it maps a contract ref to a concrete
// cluster mutation and applies it through the injected Kube seam.
type Runner struct {
	kube     Kube
	bindings map[string]binding
}

// newWith is the seam constructor shared by production (New, over a
// liveKube) and tests (NewWith, over a fake). The binding map is the one
// place in the system that knows a contract ref's concrete mutation.
func newWith(k Kube) *Runner {
	return &Runner{kube: k, bindings: bindingSet()}
}

// bindingSet is the one place in the system that knos a contract
// ref's concrete mutation and it's undo.  Every Runner shares it,
// and BoundRefs reads its keys without needing a live Kube.
func bindingSet() map[string]binding {
	const rookCeph = "rook-ceph"
	toolbox := func(argv ...string) execOp {
		return execOp{
			namespace: rookCeph,
			selector:  "app=rook-ceph-tools",
			command:   argv,
		}
	}
	rgwPatch := func(instances int) patchOp {
		return patchOp{
			group:     "ceph.rook.io",
			version:   "v1",
			resource:  "cephobjectstores",
			namespace: rookCeph,
			name:      "ceph-objectstore",
			patch:     []byte(fmt.Sprintf(`{"spec":{"gateway":{"instances":%d}}}`, instances)),
		}
	}
	return map[string]binding{
		"hold-rebalance": {
			forward: toolbox("ceph", "osd", "set", "noout"),
			reverse: toolbox("ceph", "osd", "unset", "noout"),
		},
		"scale-out-rgw-gateways": {
			forward: rgwPatch(2),
			reverse: rgwPatch(1),
		},
	}
}

// Run dispatches ref's forward (or reverse) mutation through the Kube seam.
// An unbound ref is an error, not a silent no-op — thump records it as a
// failure with text.
func (r *Runner) Run(ctx context.Context, ref string, reverse bool, _ map[string]float64) error {
	b, ok := r.bindings[ref]
	if !ok {
		return fmt.Errorf("actuate: ref %q is not bound to an action", ref)
	}
	op := b.forward
	if reverse {
		op = b.reverse
	}
	if err := op.do(ctx, r.kube); err != nil {
		return fmt.Errorf("actuate: %s (reverse=%v): %w", ref, reverse, err)
	}
	return nil
}

// BoundRefs returns the contract refs the actuator can actually
// execute, sorted for a stable test.
func BoundRefs() []string {
	return slices.Sorted(maps.Keys(bindingSet()))
}
