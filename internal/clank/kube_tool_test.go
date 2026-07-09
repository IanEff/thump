package clank_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
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
			tool := &clank.KubeTool{Client: clientset}

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
