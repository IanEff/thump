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
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/forge"
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
	do(ctx context.Context, d dispatch) error
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

func (e execOp) do(ctx context.Context, d dispatch) error {
	return d.kube.Exec(ctx, e.namespace, e.selector, e.command)
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

func (f flagVariantOp) do(ctx context.Context, d dispatch) error {
	current, err := d.kube.GetConfigMapKey(ctx, f.namespace, f.configMap, f.dataKey)
	if err != nil {
		return fmt.Errorf("read %s/%s[%s]: %w", f.namespace, f.configMap, f.dataKey, err)
	}

	updated, err := setDefaultVariant([]byte(current), f.flag, f.variant)
	if err != nil {
		return fmt.Errorf("%s/%s[%s]: %w", f.namespace, f.configMap, f.dataKey, err)
	}

	patch, err := json.Marshal(map[string]any{"data": map[string]string{f.dataKey: string(updated)}})
	if err != nil {
		return fmt.Errorf("build merge patch for %s/%s: %w", f.namespace, f.configMap, err)
	}

	return d.kube.Patch(ctx, "", "v1", "configmaps", f.namespace, f.configMap, patch)
}

// restartOp triggers a rolling restart of a Deployment by merge-patching a
// timestamp annotation onto its pod template — the same mechanism `kubectl
// rollout restart` uses under the hood, expressed through the existing Patch
// primitive rather than a new Kube method. Authored for cart's "plausible
// but wrong" second remedy (Wave 7): it recycles cart's pods, which does
// nothing for a flagd-controlled fault, so it never actually clears
// cartFailure — see restart-cart-pod's low authored SeverityReductionPct in
// config/actions/catalog.yaml.
type restartOp struct {
	namespace, deployment string
}

func (r restartOp) do(ctx context.Context, d dispatch) error {
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"thump.io/restartedAt": time.Now().UTC().Format(time.RFC3339),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("build merge patch for %s/%s restart: %w", r.namespace, r.deployment, err)
	}
	return d.kube.Patch(ctx, "apps", "v1", "deployments", r.namespace, r.deployment, patch)
}

// binding is a ref's forward mutation and its authored undo.
type binding struct {
	forward operation
	reverse operation
}

// maintenanceReleaseOp rewrites one flagd flag in the GitOps source of record
// and leaves a release for review.
type maintenanceReleaseOp struct {
	path, flag, variant string
}

func (m maintenanceReleaseOp) do(ctx context.Context, d dispatch) error {
	if d.forge == nil {
		return fmt.Errorf("no forge wired for %s: %w", m.path, ErrUnbindable)
	}
	current, err := d.forge.Read(ctx, m.path)
	if err != nil {
		return fmt.Errorf("read %s: %w", m.path, err)
	}

	updated, err := setDefaultVariant(current, m.flag, m.variant)
	if err != nil {
		return fmt.Errorf("read %s: %w", m.path, err)
	}

	_, err = d.forge.Cut(ctx, forge.Release{
		Key: releaseKey(d.ref, d.reverse), Path: m.path, Content: updated, Notes: d.notes,
	})

	return err
}

// actuateTimeout bounds one Runner.Run call. Transport.handle (thump.go)
// never calls broker.Handler's heartbeat for the live-execute path — that
// choice assumes rendering-and-executing stays fast, which client-go's own
// defaults don't guarantee, since rest.InClusterConfig sets no Timeout and
// leaves an apiserver call to hang on a wedged connection indefinitely. Left
// unbounded, a hung mutation would sit past the consumer's 30s AckWait
// (broker.go), get redelivered, and run the same live mutation a second time
// while the first is still stuck. actuateTimeout fails it first, as one
// ordinary Outcome, instead.
const actuateTimeout = 20 * time.Second

// Runner satisfies thump.ActionRunner: it maps a contract ref to a concrete
// cluster mutation and applies it through the injected Kube seam.
type Runner struct {
	kube     Kube
	forge    Forge
	bindings map[string]binding
	timeout  time.Duration // bounds one Run call; see actuateTimeout
}

// newWith is the seam constructor shared by production (New, over a
// liveKube) and tests (NewWith, over a fake). Every ref a Runner can execute
// is bound from cat, so nothing outside the authored catalog is reachable.
func newWith(k Kube, cat *contract.StaticCatalog) (*Runner, error) {
	return newWithTimeout(k, cat, actuateTimeout, nil)
}

// newWithTimeout is newWith with the Run bound overridable — production and
// ordinary tests always go through newWith's fixed actuateTimeout; only a
// test proving the bound itself needs a shorter one to stay fast.
func newWithTimeout(k Kube, cat *contract.StaticCatalog, timeout time.Duration, forge Forge) (*Runner, error) {
	b, err := bind(cat, forge != nil)
	if err != nil {
		return nil, err
	}
	return &Runner{kube: k, forge: forge, bindings: b, timeout: timeout}, nil
}

// Run dispatches ref's forward (or reverse) mutation through the Kube seam,
// cut off at r.timeout. An unbound ref is an error, not a silent no-op —
// thump records it as a failure with text, same as a timed-out or failing
// mutation does. The Result reported is the dispatched op's own — see
// resultOf — because a maintenanceRelease op ran fine and mutated nothing;
// reporting ResultApplied for it would start transport.go's convergence
// watcher against a change nobody has accepted.
func (r *Runner) Run(ctx context.Context, ref string, reverse bool, _ map[string]float64, notes string) (outcome.Result, error) {
	b, ok := r.bindings[ref]
	if !ok {
		return "", fmt.Errorf("actuate: ref %q is not bound to an action", ref)
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	op := b.forward
	if reverse {
		op = b.reverse
	}

	d := dispatch{kube: r.kube, forge: r.forge, ref: ref, reverse: reverse, notes: notes}
	if err := op.do(ctx, d); err != nil {
		return "", fmt.Errorf("actuate: %s (reverse=%v): %w", ref, reverse, err)
	}
	return resultOf(op), nil
}

// resultOf reports what kind of op actually ran: every op mutates the
// cluster directly except maintenanceReleaseOp, which leaves an artifact
// for review instead. A seqOp's result is its last step's, since that's the
// one still pending review (or the one that mutated) once the sequence
// finishes.
func resultOf(op operation) outcome.Result {
	switch o := op.(type) {
	case maintenanceReleaseOp:
		return outcome.ResultProposed
	case seqOp:
		if len(o) == 0 {
			return outcome.ResultApplied
		}
		return resultOf(o[len(o)-1])
	default:
		return outcome.ResultApplied
	}
}

// scaleOp merge-patches a Deployment's spec.replicas to a fixed count — the
// same Patch primitive restartOp uses. Forward and reverse are both authored
// as literal replica counts, not a delta, so the reversal is deterministic
// rather than remembered.
type scaleOp struct {
	namespace, deployment string
	replicas              int
}

func (s scaleOp) do(ctx context.Context, d dispatch) error {
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{"replicas": s.replicas},
	})
	if err != nil {
		return fmt.Errorf("build merge patch for %s/%s scale: %w", s.namespace, s.deployment, err)
	}
	return d.kube.Patch(ctx, "apps", "v1", "deployments", s.namespace, s.deployment, patch)
}

// seqOp runs its operations in order, stopping at the first failure —
// sequencing is a property of the authored step list, so a multi-step remedy
// needs no verb of its own.
type seqOp []operation

func (s seqOp) do(ctx context.Context, d dispatch) error {
	for _, op := range s {
		if err := op.do(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

// dispatch is what one Run call hands its operation.
type dispatch struct {
	kube    Kube
	forge   Forge
	ref     string // the authored contract ref
	reverse bool   // true for an undo — see releaseKey
	notes   string // thump's rendering of the ranked set, empty for a mutation.
}

type Forge interface {
	// Read returns the current bytes at path on the default branch.
	Read(ctx context.Context, path string) ([]byte, error)
	// Cut publishes rel for review and returns where a human can find it.
	Cut(ctx context.Context, rel forge.Release) (url string, err error)
	// Withdraw retracts the release for key if it is still open, and
	// reports whether it had already been accepted.
	Withdraw(ctx context.Context, key string) (accepted bool, err error)
}

// setDefaultVariant returns doc with flag's defaultVariant set to variant.
func setDefaultVariant(doc []byte, flag, variant string) ([]byte, error) {
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		return nil, fmt.Errorf("parse flagd document: %w", err)
	}
	flags, _ := parsed["flags"].(map[string]any)

	def, ok := flags[flag].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("flag %q not defined in flagd document", flag)
	}
	def["defaultVariant"] = variant

	return json.Marshal(parsed)
}

// releaseKey is one open release's identity — a redelivery in the same
// direction collapses onto it, but a revert is a second review against the
// same path, so it keys separately or it would silently rewrite the forward
// release instead of undoing it.
func releaseKey(ref string, reverse bool) string {
	if reverse {
		return ref + ":revert"
	}
	return ref
}
