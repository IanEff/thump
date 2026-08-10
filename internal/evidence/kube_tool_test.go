package evidence_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/subjects"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubeTool_Run(t *testing.T) {
	tests := map[string]struct {
		input   string
		pods    []*corev1.Pod
		wantRef proposal.EvidenceRef
	}{
		"Run given a pod query returns live evidence summary": {
			input: `{"resource": "pods", "namespace": "rook-ceph"}`,
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "osd-0", Namespace: "rook-ceph"},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "osd-1", Namespace: "rook-ceph"},
					Status:     corev1.PodStatus{Phase: corev1.PodFailed},
				},
			},
			wantRef: proposal.EvidenceRef{
				Tool:    "kube",
				Query:   `{"resource": "pods", "namespace": "rook-ceph"}`,
				Summary: "osd-0 (Running), osd-1 (Failed)",
				Ref:     "kube://rook-ceph/pods",
				Live:    true,
			},
		},
		"Run given an empty namespace returns non-live evidence": {
			input: `{"resource": "pods", "namespace": "empty-ns"}`,
			pods:  nil,
			wantRef: proposal.EvidenceRef{
				Tool:    "kube",
				Query:   `{"resource": "pods", "namespace": "empty-ns"}`,
				Summary: "no pods found",
				Live:    false,
			},
		},
		"Run given an unsupported resource returns non-live evidence": {
			input: `{"resource": "deployments", "namespace": "default"}`,
			wantRef: proposal.EvidenceRef{
				Tool:    "kube",
				Query:   `{"resource": "deployments", "namespace": "default"}`,
				Summary: "unsupported resource: deployments",
				Live:    false,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange: seed the fake cluster
			var objs []runtime.Object
			for _, p := range tc.pods {
				objs = append(objs, p)
			}
			clientset := fake.NewSimpleClientset(objs...)
			tool := &evidence.KubeTool{Client: clientset}

			// Act
			gotRef, err := tool.Run(context.Background(), json.RawMessage(tc.input))
			// Assert
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantRef, gotRef); diff != "" {
				t.Error("KubeTool.Run returned wrong EvidenceRef", diff)
			}
		})
	}
}

// TestKubeTool_Run_ListsOnlyPodsMatchingTheSelector pins the narrowing a
// subject claim depends on. A namespace holding several topology nodes —
// rook-ceph, otel-demo — cannot be evidence about any one of them, so without
// a selector every kube citation is a multi-node aggregate and can never
// ground a proposal.
func TestKubeTool_Run_ListsOnlyPodsMatchingTheSelector(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "osd-0", Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "mon-a", Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-mon"}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	tool := &evidence.KubeTool{Client: clientset}

	got, err := tool.Run(t.Context(), json.RawMessage(
		`{"resource":"pods","namespace":"rook-ceph","selector":{"app":"rook-ceph-osd"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff("osd-0 (Running)", got.Summary); diff != "" {
		t.Error("the summary must name only the pods the selector matched (-want +got)\n", diff)
	}
}

// TestKubeTool_Run_StampsTheSubjectItsCoordinatesResolveTo pins the tag
// without which a cluster citation is inert: gate.go's coherentSubject fails
// closed on an untagged ref, so a kube ref with no Subject can never ground a
// proposal on its own and never counts toward the two-backend corroboration
// floor. The tag comes from authored rules and the query's own coordinates,
// never from the model.
func TestKubeTool_Run_StampsTheSubjectItsCoordinatesResolveTo(t *testing.T) {
	t.Parallel()

	index := subjects.SubjectIndex{
		{Subject: "ceph-osd", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}}},
	}
	pods := []runtime.Object{
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "osd-0", Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
	}

	tests := map[string]struct {
		input string
		want  proposal.EvidenceRef
	}{
		"Run stamps the subject a matching rule names on a live citation": {
			input: `{"resource":"pods","namespace":"rook-ceph","selector":{"app":"rook-ceph-osd"}}`,
			want: proposal.EvidenceRef{
				Tool:    "kube",
				Query:   `{"resource":"pods","namespace":"rook-ceph","selector":{"app":"rook-ceph-osd"}}`,
				Summary: "osd-0 (Running)",
				Ref:     "kube://rook-ceph/pods",
				Live:    true,
				Subject: "ceph-osd",
			},
		},
		"Run stamps the subject even when the selector matched no pods": {
			input: `{"resource":"pods","namespace":"rook-ceph","selector":{"app":"rook-ceph-osd","tier":"cold"}}`,
			want: proposal.EvidenceRef{
				Tool:    "kube",
				Query:   `{"resource":"pods","namespace":"rook-ceph","selector":{"app":"rook-ceph-osd","tier":"cold"}}`,
				Summary: "no pods found",
				Live:    false,
				Subject: "ceph-osd",
			},
		},
		"Run stamps no subject for a namespace-wide query the rules cannot narrow": {
			input: `{"resource":"pods","namespace":"rook-ceph"}`,
			want: proposal.EvidenceRef{
				Tool:    "kube",
				Query:   `{"resource":"pods","namespace":"rook-ceph"}`,
				Summary: "osd-0 (Running)",
				Ref:     "kube://rook-ceph/pods",
				Live:    true,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tool := &evidence.KubeTool{Client: fake.NewSimpleClientset(pods...), Subjects: index}

			got, err := tool.Run(t.Context(), json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong EvidenceRef for the query's coordinates (-want +got)\n", diff)
			}
		})
	}
}

// TestKubeTool_SpecAdvertisesEveryAuthoredPodSelectorRatherThanOneWorkedExample
// pins that Spec renders the rig's whole authored selector set, not one
// arbitrary first rule — a model choosing among several workloads needs to
// see all of them, not guess the rest from a single example.
func TestKubeTool_SpecAdvertisesEveryAuthoredPodSelectorRatherThanOneWorkedExample(t *testing.T) {
	t.Parallel()

	tool := &evidence.KubeTool{Subjects: subjects.SubjectIndex{
		{Subject: "ceph-osd", Plane: "kube", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-osd"}}},
		{Subject: "ceph-rgw", Plane: "kube", Coordinates: subjects.Coordinates{Namespace: "rook-ceph", Labels: map[string]string{"app": "rook-ceph-rgw"}}},
		{Subject: "cart", Plane: "loki", Coordinates: subjects.Coordinates{Namespace: "otel-demo", Labels: map[string]string{"service_name": "cart"}}},
	}}

	desc := tool.Spec().Description

	if !strings.Contains(desc, "ceph-osd: namespace=rook-ceph, app=rook-ceph-osd") {
		t.Error("kube Spec description missing the ceph-osd selector", desc)
	}
	if !strings.Contains(desc, "ceph-rgw: namespace=rook-ceph, app=rook-ceph-rgw") {
		t.Error("kube Spec description missing the ceph-rgw selector", desc)
	}
	if strings.Contains(desc, "cart") {
		t.Error("kube Spec description advertised a loki-only selector", desc)
	}
}
