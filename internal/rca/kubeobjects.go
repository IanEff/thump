package rca

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// kubeObject is one pod config/<rig>/rca/kube-objects.yaml declares to seed
// the graded suite's kube fake — the topology objects a rig's
// evidence-queries.yaml selectors need present to resolve, authored per rig
// because which pods exist on it is a property of its deployment, never of
// this package.
type kubeObject struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
	Phase     string            `json:"phase"`
}

// loadKubeObjects parses config/<rig>/rca/kube-objects.yaml. A rig that
// names no such file grades with an empty kube fake, never an error — not
// every rig's evidence-queries.yaml selectors need one.
func loadKubeObjects(path string) ([]runtime.Object, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied rig config path, not user input
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read kube objects %s: %w", path, err)
	}

	var file struct {
		Pods []kubeObject `json:"pods"`
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse kube objects %s: %w", path, err)
	}

	objs := make([]runtime.Object, len(file.Pods))
	for i, p := range file.Pods {
		objs[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace, Labels: p.Labels},
			Status:     corev1.PodStatus{Phase: corev1.PodPhase(p.Phase)},
		}
	}
	return objs, nil
}
