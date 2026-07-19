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
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Kube is the impure seam actuate reaches the cluster through — exec a
// command inside a pod (selected by label), merge-patch a named custom
// resource, or read one ConfigMap data key back. Expressed in primitives,
// not client-go types, so the fake in tests is trivial and the pure core
// above stays free of any apiserver import. Production wires a liveKube
// (kube.go); tests inject a recorder.
type Kube interface {
	// Exec runs command inside the first Running pod matching selector in
	// namespace, returning an error (with captured stderr) if it fails. No
	// shell is involved — command is argv handed straight to the container.
	Exec(ctx context.Context, namespace, selector string, command []string) error
	// Patch applies a merge patch to the named resource identified by its
	// group/version/resource in namespace.
	Patch(ctx context.Context, group, version, resource, namespace, name string, mergePatch []byte) error
	// GetConfigMapKey returns the string value stored at key in the named
	// ConfigMap's data — the read half of a read-modify-write flip, needed
	// whenever the desired patch can't be expressed as static bytes because
	// it depends on the resource's current state (unlike Patch's
	// caller-supplied literal).
	GetConfigMapKey(ctx context.Context, namespace, name, key string) (string, error)
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

// execSeqOp runs several toolbox commands in order, stopping at the first
// failure — for a mutation ceph splits across calls (configure, then
// enable) that a single execOp can't express.
type execSeqOp []execOp

func (s execSeqOp) do(ctx context.Context, k Kube) error {
	for _, e := range s {
		if err := e.do(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

// flagVariantOp flips one flagd flag's defaultVariant by reading the target
// ConfigMap's JSON-blob data key, editing that one field, and merge-patching
// the whole blob back — the same read-modify-write the thump-test rig's own
// chaos/_flagd.sh performs by hand (kubectl get | jq | kubectl patch), and
// the reason this can't be a plain Patch: the ConfigMap's `data` value is an
// opaque JSON string, not structured fields a merge patch can reach inside
// without first knowing every other flag's current state.
type flagVariantOp struct {
	namespace, configMap, dataKey, flag, variant string
}

func (f flagVariantOp) do(ctx context.Context, k Kube) error {
	current, err := k.GetConfigMapKey(ctx, f.namespace, f.configMap, f.dataKey)
	if err != nil {
		return fmt.Errorf("read %s/%s[%s]: %w", f.namespace, f.configMap, f.dataKey, err)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(current), &doc); err != nil {
		return fmt.Errorf("parse %s/%s[%s]: %w", f.namespace, f.configMap, f.dataKey, err)
	}
	flags, _ := doc["flags"].(map[string]any)
	def, ok := flags[f.flag].(map[string]any)
	if !ok {
		return fmt.Errorf("flag %q not defined in %s/%s[%s]", f.flag, f.namespace, f.configMap, f.dataKey)
	}
	def["defaultVariant"] = f.variant

	updated, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal %s/%s[%s]: %w", f.namespace, f.configMap, f.dataKey, err)
	}
	patch, err := json.Marshal(map[string]any{"data": map[string]string{f.dataKey: string(updated)}})
	if err != nil {
		return fmt.Errorf("build merge patch for %s/%s: %w", f.namespace, f.configMap, err)
	}

	return k.Patch(ctx, "", "v1", "configmaps", f.namespace, f.configMap, patch)
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
	// flagd flips the thump-test rig's OTel demo faults — the flagd-config
	// ConfigMap lives in otel-demo, keyed by demo.flagd.json (matches
	// chaos/_flagd.sh in the rig repo exactly). forward disables the named
	// failure (variant "off", the fix); reverse re-arms it (variant "on").
	const flagdNamespace, flagdConfigMap, flagdDataKey = "otel-demo", "flagd-config", "demo.flagd.json"
	flagd := func(flag string) binding {
		return binding{
			forward: flagVariantOp{namespace: flagdNamespace, configMap: flagdConfigMap, dataKey: flagdDataKey, flag: flag, variant: "off"},
			reverse: flagVariantOp{namespace: flagdNamespace, configMap: flagdConfigMap, dataKey: flagdDataKey, flag: flag, variant: "on"},
		}
	}
	return map[string]binding{
		"hold-rebalance": {
			forward: toolbox("ceph", "osd", "set", "noout"),
			reverse: toolbox("ceph", "osd", "unset", "noout"),
		},
		// forward raises recovery concurrency far above Ceph's defaults (1
		// backfill, 3 recovery ops); reverse removes the overrides so the
		// cluster returns to its compiled defaults, not a guessed number.
		"accelerate-recovery": {
			forward: execSeqOp{
				toolbox("ceph", "config", "set", "osd", "osd_max_backfills", "16"),
				toolbox("ceph", "config", "set", "osd", "osd_recovery_max_active", "16"),
			},
			reverse: execSeqOp{
				toolbox("ceph", "config", "rm", "osd", "osd_max_backfills"),
				toolbox("ceph", "config", "rm", "osd", "osd_recovery_max_active"),
			},
		},
		"disable-product-catalog-failure": flagd("productCatalogFailure"),
		"disable-cart-failure":            flagd("cartFailure"),
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
