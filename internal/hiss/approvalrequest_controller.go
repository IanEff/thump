package hiss

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ianeff/thump/api/v1/approval"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/publish"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	approvalRequestGroup   = "thump.dev"
	approvalRequestVersion = "v1alpha1"
	approvalRequestKind    = "ApprovalRequest"
	approvalRequestPlural  = "approvalrequests"

	// approvedByAnnotation is stamped by the approvalrequest-stamp-approver
	// MutatingAdmissionPolicy from the patch request's authenticated
	// UserInfo, overwriting whatever a client sent — it lives in an
	// annotation, not spec or status, because annotation mutations survive
	// a main-resource UPDATE and .status mutations don't (see
	// ApprovalRequestSpec's doc comment).
	approvedByAnnotation = "thump.dev/approved-by"
)

var approvalRequestGVR = schema.GroupVersionResource{
	Group:    approvalRequestGroup,
	Version:  approvalRequestVersion,
	Resource: approvalRequestPlural,
}

// ApprovalRequestController is hiss's one file that touches Kubernetes: it
// creates an ApprovalRequest CR for every hold (the reverse of
// newApprovalRequestSpec) and watches for a human's kubectl patch,
// translating spec.decision into an ack. It never evaluates or decides, and
// it never reaches thump.decisions — a patched CR can only ever satisfy the
// condition hiss already attached, leaving hiss the one place a verdict is
// issued. Substituting a human's judgment for that gate is break-glass and
// lives in trim, not here.
type ApprovalRequestController struct {
	Dyn        dynamic.Interface
	Namespace  string
	ApprovePub publish.Publisher[approval.Approval] // → thump.approvals, picked up by the existing approveHandler
	Now        func() time.Time

	// Retention bounds how long a Processed CR stays readable before the
	// controller sweeps it; zero means DefaultApprovalRequestRetention. The
	// sealed WAL is the audit record — this resource is a projection, and an
	// unswept projection is an etcd leak, one object per hold, forever.
	Retention time.Duration

	// Timeout bounds one apiserver call; zero means approvalRequestTimeout.
	Timeout time.Duration
}

// approvalRequestTimeout bounds a single apiserver call. It is deliberately
// under the broker's 30s AckWait: an unreachable apiserver must fail the
// message while the handler still owns it, so the failure surfaces as a
// logged error and a nak rather than an ack deadline expiring in silence.
// rest.InClusterConfig sets no Timeout of its own, and client-go's default
// dial timeout is itself 30s — long enough to lose the race.
const approvalRequestTimeout = 10 * time.Second

// bound returns ctx with Timeout applied — every call this controller makes
// to the apiserver goes through it, so no single unreachable API server can
// stall a governed decision past its delivery budget.
func (c *ApprovalRequestController) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = approvalRequestTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// DefaultApprovalRequestRetention leaves a decided ApprovalRequest readable
// for a day before the sweep reclaims it.
const DefaultApprovalRequestRetention = 24 * time.Hour

// NewApprovalRequestController builds the production controller: an
// in-cluster dynamic client scoped to this pod's own namespace, read from
// the standard ServiceAccount projection (already mounted — InClusterConfig
// itself depends on the token file living alongside it). Mirrors
// internal/actuate/kube.go's New: the one file allowed to import client-go
// builds its own clients and hands the composition root a ready value,
// never the other way around, so hiss.go's runBroker needs no new import at
// all — a same-package function call, not a client-go dependency.
func NewApprovalRequestController(approvePub publish.Publisher[approval.Approval]) (*ApprovalRequestController, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("hiss: in-cluster config: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("hiss: dynamic client: %w", err)
	}
	ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace") //nolint:gosec // G304: fixed well-known path, not user input
	if err != nil {
		return nil, fmt.Errorf("hiss: read own namespace: %w", err)
	}
	return &ApprovalRequestController{
		Dyn:        dyn,
		Namespace:  string(ns),
		ApprovePub: approvePub,
	}, nil
}

func (c *ApprovalRequestController) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// approvalRequestName derives a deterministic, DNS-1123-safe object name
// from a fingerprint that may carry characters Kubernetes object names
// can't — rattle's fingerprints are "kind:object" (internal/rattle/reconcile.go's
// fingerprint), e.g. "deployment:otel-cart". The raw fingerprint still
// round-trips through spec.signalRef, so nothing is lost to the hash.
func approvalRequestName(signalRef string) string {
	sum := sha256.Sum256([]byte(signalRef))
	return "ar-" + hex.EncodeToString(sum[:])[:16]
}

// Create makes the ApprovalRequest CR for a freshly held Governed — the
// human-facing side of a hold, and the reverse of newApprovalRequestSpec.
// AlreadyExists is treated as success: Holds.Record's doc comment already
// guarantees clank's dedupe ledger keeps a fingerprint from firing twice in
// practice, so a redelivered message finding its CR already there is a
// no-op, not an error.
func (c *ApprovalRequestController) Create(ctx context.Context, held decision.Governed) error {
	spec := newApprovalRequestSpec(held)
	specMap, err := toUnstructuredMap(spec)
	if err != nil {
		return fmt.Errorf("hiss: approvalrequest spec for %s: %w", spec.SignalRef, err)
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": approvalRequestGroup + "/" + approvalRequestVersion,
		"kind":       approvalRequestKind,
		"metadata": map[string]any{
			"name":      approvalRequestName(spec.SignalRef),
			"namespace": c.Namespace,
		},
		"spec": specMap,
	}}

	ctx, cancel := c.bound(ctx)
	defer cancel()

	_, err = c.Dyn.Resource(approvalRequestGVR).Namespace(c.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("hiss: create approvalrequest for %s: %w", spec.SignalRef, err)
	}
	return nil
}

// Run watches ApprovalRequest CRs until ctx is cancelled, reconciling every
// add and update. It blocks until ctx is done, matching beat.RunConsumer's
// shape for runBroker's errgroup.
func (c *ApprovalRequestController) Run(ctx context.Context) error {
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.Dyn, 10*time.Minute, c.Namespace, nil)
	informer := factory.ForResource(approvalRequestGVR).Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.reconcileObj(ctx, obj) },
		UpdateFunc: func(_, obj any) { c.reconcileObj(ctx, obj) },
	})
	if err != nil {
		return fmt.Errorf("hiss: register approvalrequest handler: %w", err)
	}
	informer.Run(ctx.Done())
	return nil
}

func (c *ApprovalRequestController) reconcileObj(ctx context.Context, obj any) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	if swept, err := c.sweep(ctx, u); err != nil {
		slog.Error("approvalrequest sweep failed", "name", u.GetName(), "err", err)
		return
	} else if swept {
		return
	}
	if err := c.reconcile(ctx, u); err != nil {
		slog.Error("approvalrequest reconcile failed", "name", u.GetName(), "err", err)
	}
}

// sweep deletes a Processed CR once it is older than Retention, reporting
// whether it did. The informer redelivers every object on every resync, so
// the resync this controller already runs is the whole scheduler the sweep
// needs — no timer, no finalizer, no second control loop.
func (c *ApprovalRequestController) sweep(ctx context.Context, u *unstructured.Unstructured) (bool, error) {
	var status ApprovalRequestStatus
	if err := fromUnstructuredField(u.Object, "status", &status); err != nil {
		return false, fmt.Errorf("decode status: %w", err)
	}
	if status.Phase != "Processed" || status.DecidedAt.IsZero() {
		return false, nil
	}

	retention := c.Retention
	if retention <= 0 {
		retention = DefaultApprovalRequestRetention
	}
	if c.now().Sub(status.DecidedAt) < retention {
		return false, nil
	}

	ctx, cancel := c.bound(ctx)
	defer cancel()

	err := c.Dyn.Resource(approvalRequestGVR).Namespace(c.Namespace).Delete(ctx, u.GetName(), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("delete %s: %w", u.GetName(), err)
	}
	return true, nil
}

// reconcile is the whole controller in one function: decode, skip if
// there's nothing new, translate, publish, mark done. status.Phase ==
// "Processed" short-circuits every branch below it because an informer
// redelivers every object on every resync for as long as hiss runs —
// without that check a long-lived process re-publishes the same ack once
// per resync, forever.
func (c *ApprovalRequestController) reconcile(ctx context.Context, u *unstructured.Unstructured) error {
	var spec ApprovalRequestSpec
	if err := fromUnstructuredField(u.Object, "spec", &spec); err != nil {
		return fmt.Errorf("decode spec: %w", err)
	}
	var status ApprovalRequestStatus
	if err := fromUnstructuredField(u.Object, "status", &status); err != nil {
		return fmt.Errorf("decode status: %w", err)
	}

	if status.Phase == "Processed" || spec.Decision == "" {
		return nil // most reconcile events land here — not an error, just nothing new yet
	}

	// The stamp is the whole reason this resource exists: an ack whose
	// approver is a string a client chose is no better than the one trim
	// already emits. Missing means the admission policies are absent or were
	// bypassed, so refuse rather than publish an unattributed approval.
	approvedBy := u.GetAnnotations()[approvedByAnnotation]
	if approvedBy == "" {
		slog.Error("approvalrequest decided with no authenticated approver — refusing to publish",
			"name", u.GetName(), "signalRef", spec.SignalRef, "annotation", approvedByAnnotation)
		return nil
	}

	ack, ok, err := translateDecision(spec, approvedBy, c.now())
	if err != nil {
		// The CRD's spec.decision enum should make this unreachable in a
		// real cluster; left unmarked so a schema/code drift retries
		// rather than silently freezing the CR in this state.
		return err
	}
	if !ok {
		return nil
	}
	if err := c.ApprovePub.Publish(ctx, "thump.approvals", ack); err != nil {
		return fmt.Errorf("publish approval for %s: %w", spec.SignalRef, err)
	}

	return c.markProcessed(ctx, u.GetName())
}

func (c *ApprovalRequestController) markProcessed(ctx context.Context, name string) error {
	patch, err := json.Marshal(map[string]any{
		"status": map[string]any{
			"phase":     "Processed",
			"decidedAt": c.now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal status patch: %w", err)
	}
	ctx, cancel := c.bound(ctx)
	defer cancel()

	_, err = c.Dyn.Resource(approvalRequestGVR).Namespace(c.Namespace).
		Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}, "status")
	if err != nil {
		return fmt.Errorf("patch %s status: %w", name, err)
	}
	return nil
}

// toUnstructuredMap and fromUnstructuredField move ApprovalRequestSpec and
// ApprovalRequestStatus through unstructured content via their JSON tags —
// simpler than runtime.DefaultUnstructuredConverter for two small, flat
// structs, and it's the only conversion this file needs.
func toUnstructuredMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func fromUnstructuredField(obj map[string]any, field string, v any) error {
	sub, ok := obj[field]
	if !ok {
		return nil // absent field: zero value stands, e.g. a freshly created CR has no status yet
	}
	raw, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
