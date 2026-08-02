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

// managed is one entry of the resource inventory ArgoCD publishes on an
// Application — the coordinates a subject rule matches against.
type managed struct {
	kind      string
	namespace string
	name      string
}

// newApplication builds a minimal Application CRD instance — only the
// fields ArgoChangeSource.Changes actually reads.
func newApplication(namespace, name string, operationState map[string]any, resources ...managed) *unstructured.Unstructured {
	status := map[string]any{"operationState": operationState}
	if len(resources) > 0 {
		inventory := make([]any, 0, len(resources))
		for _, r := range resources {
			inventory = append(inventory, map[string]any{"kind": r.kind, "namespace": r.namespace, "name": r.name})
		}
		status["resources"] = inventory
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
		apps     []*unstructured.Unstructured
		subjects clank.SubjectIndex
		now      time.Time
		want     proposal.ChangeSnapshot
	}{
		"Changes names the topology node a synced resource belongs to rather than the Kubernetes object": {
			// The two vocabularies genuinely differ: ArgoCD reports the
			// CephBlockPool "replicapool", the catalog holds the entity
			// "cephblockpool". Both are strings, so an untranslated target
			// joins against nothing while every test stays green.
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "rook-storage", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}, managed{"CephBlockPool", "rook-ceph", "replicapool"}),
			},
			subjects: clank.SubjectIndex{
				{Subject: "cephblockpool", Coordinates: clank.Coordinates{Namespace: "rook-ceph", Kind: "CephBlockPool", Name: "replicapool"}},
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cephblockpool", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
			}},
		},
		"Changes reports one event per topology node when an Application syncs several": {
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "opentelemetry-demo", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}, managed{"Deployment", "otel-demo", "cart"}, managed{"Deployment", "otel-demo", "checkout"}),
			},
			subjects: clank.SubjectIndex{
				{Subject: "cart", Coordinates: clank.Coordinates{Namespace: "otel-demo", Kind: "Deployment", Name: "cart"}},
				{Subject: "checkout", Coordinates: clank.Coordinates{Namespace: "otel-demo", Kind: "Deployment", Name: "checkout"}},
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cart", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
				{ID: "abc123", Type: "deploy", Target: "checkout", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
			}},
		},
		"Changes counts a topology node once when several of its objects synced together": {
			// A Deployment and its Service are one node changing, not two —
			// otherwise a node's causal weight scales with how many manifests
			// happen to describe it.
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "opentelemetry-demo", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}, managed{"Deployment", "otel-demo", "cart"}, managed{"Service", "otel-demo", "cart"}),
			},
			subjects: clank.SubjectIndex{{Subject: "cart", Coordinates: clank.Coordinates{Namespace: "otel-demo"}}},
			now:      time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cart", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
			}},
		},
		"Changes drops a synced resource no subject rule claims": {
			// An unresolved Kubernetes name on a target is indistinguishable
			// from a topology node that went missing, so it is not reported at
			// all rather than reported as a node nothing can place.
			apps: []*unstructured.Unstructured{
				newApplication("argocd", "cert-manager", map[string]any{
					"phase":      "Succeeded",
					"finishedAt": "2026-07-30T11:30:00Z",
					"operation":  map[string]any{"sync": map[string]any{"revision": "abc123"}},
				}, managed{"Deployment", "cert-manager", "cert-manager-webhook"}),
			},
			subjects: clank.SubjectIndex{{Subject: "cart", Coordinates: clank.Coordinates{Namespace: "otel-demo"}}},
			now:      time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "abc123", Type: "deploy", Target: "cert-manager", Age: 30 * time.Minute, HistoricalStaleness: 30 * time.Minute},
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
			subjects: clank.SubjectIndex{{Subject: "cart", Coordinates: clank.Coordinates{Namespace: "otel-demo"}}},
			now:      time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
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
				}, managed{"Deployment", "rook-ceph", "rook-ceph-operator"}),
			},
			subjects: clank.SubjectIndex{
				{Subject: "rook-operator", Coordinates: clank.Coordinates{Namespace: "rook-ceph", Kind: "Deployment", Name: "rook-ceph-operator"}},
			},
			now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "def456", Type: "rollback", Target: "rook-operator", Age: time.Minute, HistoricalStaleness: time.Minute},
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
				}, managed{"Deployment", "cert-manager", "cert-manager"}),
			},
			subjects: clank.SubjectIndex{{Subject: "cert-manager", Coordinates: clank.Coordinates{Namespace: "cert-manager"}}},
			now:      time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			want:     proposal.ChangeSnapshot{},
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
				}, managed{"Deployment", "otel-demo", "checkout"}),
			},
			subjects: clank.SubjectIndex{{Subject: "checkout", Coordinates: clank.Coordinates{Namespace: "otel-demo", Kind: "Deployment", Name: "checkout"}}},
			now:      time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
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

			src := clank.ArgoChangeSource{Client: fake, Subjects: tc.subjects, Now: func() time.Time { return tc.now }}
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
