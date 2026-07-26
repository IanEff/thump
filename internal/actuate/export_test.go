package actuate

import (
	"context"

	"github.com/ianeff/thump/internal/contract"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// NewWith exposes the fake-Kube constructor to the external test package
// without widening the production surface (production callers get New,
// which builds a real in-cluster client).
func NewWith(k Kube, cat *contract.StaticCatalog) (*Runner, error) { return newWith(k, cat) }

// FirstRunningForTest exposes firstRunning to actuate_test — pure
// pod-selection logic, the cheapest real coverage in the package.
func FirstRunningForTest(pods []corev1.Pod) (pod, container string, err error) {
	return firstRunning(pods)
}

// LiveKubeForTest builds the production Kube implementation over test
// fakes, for actuate_test. liveKube stays unexported — tests reach it only
// through this constructor and Kube's already-exported methods.
func LiveKubeForTest(cs kubernetes.Interface, dyn dynamic.Interface) Kube {
	return liveKube{cs: cs, dyn: dyn}
}

// ExecTargetForTest exposes liveKube.execTarget to actuate_test — the half
// of Exec decidable without a live SPDY stream. See kube.go's execTarget
// doc comment for why the rest of Exec is excluded from test, deliberately.
func ExecTargetForTest(ctx context.Context, cs kubernetes.Interface, namespace, selector string) (pod, container string, err error) {
	return liveKube{cs: cs}.execTarget(ctx, namespace, selector)
}
