package actuate

import (
	"context"
	"time"

	"github.com/ianeff/thump/internal/contract"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// NewWith exposes the fake-Kube constructor to the external test package
// without widening the production surface (production callers get New,
// which builds a real in-cluster client).
func NewWith(k Kube, cat *contract.StaticCatalog) (*Runner, error) { return newWith(k, cat, nil) }

// NewWithTimeoutForTest exposes newWithTimeout so a test can shrink
// actuateTimeout down to something synctest can advance instantly, rather
// than waiting out the real production bound.
func NewWithTimeoutForTest(k Kube, cat *contract.StaticCatalog, timeout time.Duration, forge Forge) (*Runner, error) {
	return newWithTimeout(k, cat, timeout, forge)
}

// NewWithForgeForTest is NewWith plus a Forge, for a test that dispatches a
// maintenanceRelease-bound ref or otherwise needs the full shipped catalog to
// bind — the shipped catalog now names one, so every Runner built over it
// needs a forge to construct at all, fake or real.
func NewWithForgeForTest(k Kube, forge Forge, cat *contract.StaticCatalog) (*Runner, error) {
	return newWithTimeout(k, cat, actuateTimeout, forge)
}

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

// ActuateTimeoutForTest exposes the bound on one Runner.Run call.
func ActuateTimeoutForTest() time.Duration { return actuateTimeout }

// BindForTest exposes bind to actuate_test — pure catalog binding resolution
// without constructing a full Runner.
func BindForTest(cat *contract.StaticCatalog, forgeWired bool) (map[string]binding, error) {
	return bind(cat, forgeWired)
}
