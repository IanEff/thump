package hiss

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
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
	"k8s.io/client-go/tools/cache"
)

const (
	approvalRequestGroup   = "thump.dev"
	approvalRequestVersion = "v1"
	approvalRequestKind    = "ApprovalRequest"
	approvalRequestPlural  = "approvalrequests"
)

var approvalRequestGVR = schema.GroupVersionResource{
	Group:    approvalRequestGroup,
	Version:  approvalRequestVersion,
	Resource: approvalRequestPlural,
}

// ApprovalRequestController is hiss's one file that touches Kubernetes: it
// creates an ApprovalRequest CR for every hold (the reverse of
// newApprovalRequestSpec) and watches for a human's kubectl patch,
// translating spec.decision back into hiss's existing NATS-side ack paths.
// It never evaluates or decides — the same authority boundary trim already
// holds, expressed as a CR instead of a CLI flag.
type ApprovalRequestController struct {
	Dyn        dynamic.Interface
	Namespace  string
	Holds      *PendingHolds
	ApprovePub publish.Publisher[approval.Approval] // → thump.approvals, picked up by the existing approveHandler
	ForcePub   publish.Publisher[decision.Governed] // → thump.decisions, mirrors trim force's break-glass path
	Now        func() time.Time
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

	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": approvalRequestGroup + "/" + approvalRequestVersion,
		"kind":       approvalRequestKind,
		"metadata": map[string]interface{}{
			"name":      approvalRequestName(spec.SignalRef),
			"namespace": c.Namespace,
		},
		"spec": specMap,
	}}

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
		AddFunc:    func(obj interface{}) { c.reconcileObj(ctx, obj) },
		UpdateFunc: func(_, obj interface{}) { c.reconcileObj(ctx, obj) },
	})
	if err != nil {
		return fmt.Errorf("hiss: register approvalrequest handler: %w", err)
	}
	informer.Run(ctx.Done())
	return nil
}

func (c *ApprovalRequestController) reconcileObj(ctx context.Context, obj interface{}) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	if err := c.reconcile(ctx, u); err != nil {
		slog.Error("approvalrequest reconcile failed", "name", u.GetName(), "err", err)
	}
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

	switch spec.Decision {
	case "approve":
		ack, ok, err := translateDecision(spec, status.ApprovedBy, c.now())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := c.ApprovePub.Publish(ctx, "thump.approvals", ack); err != nil {
			return fmt.Errorf("publish approval for %s: %w", spec.SignalRef, err)
		}
	case "force":
		held, ok := c.Holds.Take(spec.SignalRef)
		if !ok {
			// Retrying won't help — the hold is gone, and it won't come
			// back on the next resync — so this still counts as done.
			slog.Warn("approvalrequest force arrived for an unheld fingerprint", "signalRef", spec.SignalRef)
			return c.markProcessed(ctx, u.GetName())
		}
		g, err := forceDecision(held, status.ApprovedBy, c.now())
		if err != nil {
			return fmt.Errorf("force %s: %w", spec.SignalRef, err)
		}
		if err := c.ForcePub.Publish(ctx, "thump.decisions", g); err != nil {
			return fmt.Errorf("publish forced decision for %s: %w", spec.SignalRef, err)
		}
	default:
		// The CRD's spec.decision enum should make this unreachable in a
		// real cluster; left unmarked so a schema/code drift retries
		// rather than silently freezing the CR in this state.
		return fmt.Errorf("unrecognized decision %q", spec.Decision)
	}

	return c.markProcessed(ctx, u.GetName())
}

func (c *ApprovalRequestController) markProcessed(ctx context.Context, name string) error {
	patch, err := json.Marshal(map[string]interface{}{
		"status": map[string]interface{}{
			"phase":     "Processed",
			"decidedAt": c.now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal status patch: %w", err)
	}
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
func toUnstructuredMap(v interface{}) (map[string]interface{}, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func fromUnstructuredField(obj map[string]interface{}, field string, v interface{}) error {
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
