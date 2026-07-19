package actuate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// liveKube is the production Kube seam — every client-go call in the package
// lives here, so runner.go's dispatch logic stays free of apiserver types
// and drivable by a fake. It carries the typed clientset (for exec), the
// dynamic client (for patching arbitrary CRs like CephObjectStore), and the
// rest.Config the SPDY exec stream needs.
type liveKube struct {
	cs  kubernetes.Interface
	dyn dynamic.Interface
	cfg *rest.Config
}

// New returns a production Runner backed by the in-cluster ServiceAccount.
// It fails rather than silently degrading when there's no in-cluster config
// — a live executor that can't reach the apiserver should refuse to start,
// not render every action a failure at runtime.
func New() (*Runner, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("actuate: in-cluster config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("actuate: kubernetes client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("actuate: dynamic client: %w", err)
	}
	return newWith(liveKube{cs: cs, dyn: dyn, cfg: cfg}), nil
}

// Exec streams command into the first Running pod matching selector, over
// the apiserver's pods/exec subresource — the same mechanism `kubectl exec`
// uses. The command runs in the pod's first container (the toolbox is
// single-container), so no container name is hardcoded. A non-zero exit or
// transport error surfaces with the captured stderr attached.
func (l liveKube) Exec(ctx context.Context, namespace, selector string, command []string) error {
	pods, err := l.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list pods %q in %s: %w", selector, namespace, err)
	}
	pod, container, err := firstRunning(pods.Items)
	if err != nil {
		return fmt.Errorf("no pod for %q in %s: %w", selector, namespace, err)
	}

	req := l.cs.CoreV1().RESTClient().Post().
		Resource("pods").Name(pod).Namespace(namespace).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(l.cfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("build exec for %s/%s: %w", namespace, pod, err)
	}
	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		return fmt.Errorf("exec %v in %s/%s: %w: %s", command, namespace, pod, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Patch applies a JSON merge patch to one named custom resource via the
// dynamic client — no scheme registration needed for the CephObjectStore CR.
func (l liveKube) Patch(ctx context.Context, group, version, resource, namespace, name string, mergePatch []byte) error {
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: resource}
	_, err := l.dyn.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, mergePatch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patch %s %q in %s: %w", resource, name, namespace, err)
	}
	return nil
}

// GetConfigMapKey returns one ConfigMap's data key via the typed clientset —
// the read half of a flagd flag flip, which must inspect the blob's other
// flags before merge-patching the whole string back (see runner.go's
// flagVariantOp).
func (l liveKube) GetConfigMapKey(ctx context.Context, namespace, name, key string) (string, error) {
	cm, err := l.cs.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get configmap %s/%s: %w", namespace, name, err)
	}
	val, ok := cm.Data[key]
	if !ok {
		return "", fmt.Errorf("configmap %s/%s has no data key %q", namespace, name, key)
	}
	return val, nil
}

// firstRunning returns the name and first-container name of the first Running
// pod in the list — exec needs a concrete pod, and a label selector can match
// a terminating or pending replica mid-rollout.
func firstRunning(pods []corev1.Pod) (name, container string, err error) {
	for _, p := range pods {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		if len(p.Spec.Containers) == 0 {
			continue
		}
		return p.Name, p.Spec.Containers[0].Name, nil
	}
	return "", "", errors.New("no running pod matched selector")
}
