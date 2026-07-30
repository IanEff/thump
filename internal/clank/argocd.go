package clank

import (
	"context"
	"fmt"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// applicationGVR is the ArgoCD Application CRD's GroupVersionResource.
var applicationGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// ArgoChangeSource is the concrete ChangeSource: it lists every Application CRD
// and reports anything recently synced as a deploy or rollback event for
// CausalScorerImpl.Score to weigh against a signal's topology and timing.
type ArgoChangeSource struct {
	Client dynamic.Interface
	Now    func() time.Time
}

// Changes lists Applications across all namespaces — an
// Application-of-Applications parent or an ApplicationSet generator can
// land a child Application anywhere, so a fixed namespace would silently
// miss whichever layout a given cluster uses. It reports one ChangeEvent
// per Application whose last sync succeeded.
func (a ArgoChangeSource) Changes(ctx context.Context, _ signal.Detection) (proposal.ChangeSnapshot, error) {
	list, err := a.Client.Resource(applicationGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return proposal.ChangeSnapshot{}, fmt.Errorf("list argocd applications: %w", err)
	}

	now := time.Now
	if a.Now != nil {
		now = a.Now
	}

	var snap proposal.ChangeSnapshot
	for _, app := range list.Items {
		phase, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "phase")
		if phase != "Succeeded" {
			continue
		}
		finishedAtStr, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "finishedAt")
		finishedAt, err := time.Parse(time.RFC3339, finishedAtStr)
		if err != nil {
			continue
		}
		automated, _, _ := unstructured.NestedBool(app.Object, "status", "operationState", "operation", "initiatedBy", "automated")
		revision, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "operation", "sync", "revision")

		typ := "deploy"
		if automated {
			typ = "rollback"
		}
		snap.Events = append(snap.Events, proposal.ChangeEvent{
			ID:     revision,
			Type:   typ,
			Target: app.GetName(),
			Age:    now().Sub(finishedAt),
		})
	}
	return snap, nil
}
