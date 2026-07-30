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
// fields ArgoChangeSource.Changes actually reads.
func newApplication(namespace, name string, operationState map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"status": map[string]any{
			"operationState": operationState,
		},
	}}
}

func TestArgoChanges(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		apps []*unstructured.Unstructured
		now  time.Time
		want proposal.ChangeSnapshot
	}{
		"Changes reports a synced Application as a deploy event aged from its finishedAt": {
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "cart", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cart", Age: 30 * time.Minute},
			}},
		},
		"Changes reports a self-heal sync as a rollback rather than a deploy": {
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "rook-ceph-operator", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:59:00Z",
					"operation": map[string]any{
						"initiatedBy": map[string]any{"automated": true},
						"sync":        map[string]any{"revision": "def456"},
					},
				}),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "def456", Type: "rollback", Target: "rook-ceph-operator", Age: time.Minute},
			}},
		},
		"Changes reports no events when every Application is untouched": {
			apps: nil,
			now:  time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{},
		},
		"Changes finds an Application regardless of which namespace App-of-Apps put it in": {
			apps: []*unstructured.Unstructured{
				newApplication("team-checkout", "checkout", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:45:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "ghi789"}},
				}),
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "ghi789", Type: "deploy", Target: "checkout", Age: 15 * time.Minute},
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
