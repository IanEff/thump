package clank_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// applicationGVR mirrors clank's own unexported applicationGVR — duplicated
// here deliberately: if ArgoChangeSource's internal GVR ever drifts from
// this literal, the fake client below stops finding anything and every
// subtest here fails loudly, which is the point.
var applicationGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

// newApplication builds a minimal Application CRD instance — only the
// fields ArgoChangeSource.Changes actually reads. resourceNames become the
// status.resources inventory ArgoCD publishes for every managed object.
func newApplication(namespace, name string, operationState map[string]any, resourceNames ...string) *unstructured.Unstructured {
	status := map[string]any{"operationState": operationState}
	if len(resourceNames) > 0 {
		resources := make([]any, 0, len(resourceNames))
		for _, rn := range resourceNames {
			resources = append(resources, map[string]any{"kind": "Deployment", "namespace": namespace, "name": rn})
		}
		status["resources"] = resources
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"status": status,
	}}
}

func TestArgoChanges(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		apps []*unstructured.Unstructured
		now  time.Time
		want proposal.ChangeSnapshot
	}{
		"Changes names each resource an Application synced rather than the Application itself": {
			// The whole point of the fan-out: a topology graph knows "cart"
			// and "checkout", never the GitOps unit that deployed them, so an
			// Application-named target can never resolve to a topology node.
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "opentelemetry-demo", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}, "cart", "checkout"),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cart", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
				{ID: "abc123", Type: "deploy", Target: "checkout", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
			}},
		},
		"Changes counts a workload once when an Application manages several objects under one name": {
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "opentelemetry-demo", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}, "cart", "cart"),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cart", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
			}},
		},
		"Changes falls back to the Application name when it reports no managed resources": {
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "cart", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cart", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
			}},
		},
		"Changes reports a self-heal sync as a rollback rather than a deploy": {
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "rook-operator", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:59:00Z",
					"operation": map[string]any{
						"initiatedBy": map[string]any{"automated": true},
						"sync":        map[string]any{"revision": "def456"},
					},
				}, "rook-ceph-operator"),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "def456", Type: "rollback", Target: "rook-ceph-operator", Age: time.Minute, HistoricalStaleness: time.Minute},
			}},
		},
		"Changes drops a sync older than the lookback window": {
			// Without a window every Application that ever synced reports on
			// every detection, and the SAO the model reads grows without bound.
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "cert-manager", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T08:00:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "old000"}},
				}, "cert-manager"),
			},
			now:  time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{},
		},
		"Changes reports no events when every Application is untouched": {
			apps: nil,
			now:  time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{},
		},
		"Changes finds an Application regardless of which namespace App-of-Apps put it in": {
			apps: []*unstructured.Unstructured{
				newApplication("team-checkout", "checkout-app", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:45:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "ghi789"}},
				}, "checkout"),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "ghi789", Type: "deploy", Target: "checkout", Age: 15 * time.Minute, HistoricalStaleness: 15 * time.Minute},
			}},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			objs := make([]runtime.Object, len(tc.apps))
			for i, a := range tc.apps {
				objs[i] = a
			}
			fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
				map[schema.GroupVersionResource]string{applicationGVR: "ApplicationList"}, objs...)

			src := clank.ArgoChangeSource{Client: fake, Now: func() time.Time { return tc.now }}
			got, err := src.Changes(t.Context(), signal.Detection{})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong change snapshot assembled from the ArgoCD applications list (-want +got)\n", diff)
			}
		})
	}
}
