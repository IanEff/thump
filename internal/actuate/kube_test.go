package actuate_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/ianeff/thump/internal/actuate"
)

func running(name string, containers ...string) corev1.Pod {
	return phase(name, corev1.PodRunning, containers...)
}

func phase(name string, p corev1.PodPhase, containers ...string) corev1.Pod {
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.PodStatus{Phase: p}}
	for _, c := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: c})
	}
	return pod
}

func labelled(name, namespace string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "ceph-tools"}}},
	}
}

func flagdConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "flagd-config", Namespace: "otel-demo"},
		Data:       map[string]string{"demo.flagd.json": `{"flags":{"cartFailure":{"defaultVariant":"on"}}}`},
	}
}

func TestFirstRunning_ChoosesAnExecTargetOrRefuses(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		pods    []corev1.Pod
		want    [2]string
		wantErr bool
	}{
		"firstRunning returns the first container of a Running pod": {
			pods: []corev1.Pod{running("toolbox-a", "ceph-tools")},
			want: [2]string{"toolbox-a", "ceph-tools"},
		},
		"firstRunning refuses when the pod list is empty": {
			pods:    []corev1.Pod{},
			wantErr: true,
		},
		"firstRunning refuses when no pod is Running": {
			pods:    []corev1.Pod{phase("toolbox-a", corev1.PodPending, "ceph-tools")},
			wantErr: true,
		},
		"firstRunning refuses when the Running pod has no containers": {
			pods:    []corev1.Pod{running("toolbox-a")},
			wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pod, container, err := actuate.FirstRunningForTest(tc.pods)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("no Running pod must be a refusal, got %q/%q", pod, container)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, [2]string{pod, container}); diff != "" {
				t.Error("wrong exec target chosen", diff)
			}
		})
	}
}

func TestExecTarget_ResolvesTheSelectorToOneRunningPod(t *testing.T) {
	t.Parallel()
	cs := fake.NewClientset(
		labelled("toolbox-a", "rook-ceph", map[string]string{"app": "rook-ceph-tools"}),
		labelled("mon-a", "rook-ceph", map[string]string{"app": "rook-ceph-mon"}),
	)

	pod, _, err := actuate.ExecTargetForTest(context.Background(), cs, "rook-ceph", "app=rook-ceph-tools")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff("toolbox-a", pod); diff != "" {
		t.Error("exec resolved the wrong pod", diff)
	}
}

func TestExecTarget_RefusesWhenNoPodMatchesTheSelector(t *testing.T) {
	t.Parallel()
	cs := fake.NewClientset()
	if _, _, err := actuate.ExecTargetForTest(context.Background(), cs, "rook-ceph", "app=rook-ceph-tools"); err == nil {
		t.Error("an unmatched selector must error")
	}
}

func TestPatch_SendsAMergePatchToTheAuthoredGVR(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	dyn := dynamicfake.NewSimpleDynamicClient(scheme, flagdConfigMap())
	k := actuate.LiveKubeForTest(nil, dyn)

	if err := k.Patch(context.Background(), "", "v1", "configmaps", "otel-demo", "flagd-config",
		[]byte(`{"data":{"demo.flagd.json":"{}"}}`)); err != nil {
		t.Fatal(err)
	}

	patch, ok := dyn.Actions()[len(dyn.Actions())-1].(k8stesting.PatchAction)
	if !ok {
		t.Fatalf("last action was not a patch: %+v", dyn.Actions())
	}
	if diff := cmp.Diff(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, patch.GetResource()); diff != "" {
		t.Error("patch reached the wrong resource", diff)
	}
	if diff := cmp.Diff(types.MergePatchType, patch.GetPatchType()); diff != "" {
		t.Error("patch type drifted", diff)
	}
}

func TestGetConfigMapKey_ReadsAKeyAndRefusesAnAbsentOne(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		key     string
		want    string
		wantErr bool
	}{
		"GetConfigMapKey returns the stored blob for an authored data key": {
			key: "demo.flagd.json", want: `{"flags":{"cartFailure":{"defaultVariant":"on"}}}`,
		},
		"GetConfigMapKey errors on an absent data key rather than returning empty": {
			key: "not-a-key", wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k := actuate.LiveKubeForTest(fake.NewClientset(flagdConfigMap()), nil)

			got, err := k.GetConfigMapKey(context.Background(), "otel-demo", "flagd-config", tc.key)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("an absent key must error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong data key value", diff)
			}
		})
	}
}
