package actuate_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
	"github.com/ianeff/thump/internal/configtest"
	"github.com/ianeff/thump/internal/forge"
	"k8s.io/client-go/rest"
)

// recordKube is a fake actuate.Kube: it records the single request it was
// asked to make (exec, patch, or configmap read) so a test can assert the
// exact mutation each binding dispatches, and never touches an apiserver.
// err, when set, is returned by Exec and GetConfigMapKey so the
// failure-propagation tests can force a failure; patchErr is separate so a
// multi-step forward's scale-then-exec sequence can fail mid-sequence rather
// than always failing at the leading patch step.
type recordKube struct {
	err      error
	patchErr error

	// exec
	execNS       string
	execSelector string
	execCommand  []string
	// execs accumulates every exec dispatched, in order
	execs [][]string

	// patch
	patchGVR           [3]string // group, version, resource
	patchNS, patchName string
	patchBody          string

	// GetConfigMapKey
	getNS, getName, getKey string
	// getReturn is the canned blob GetConfigMapKey hands back; defaults to a
	// two-flag flagd-config fixture (both "on") when unset.
	getReturn string
}

func (k *recordKube) Exec(_ context.Context, namespace, selector string, command []string) error {
	k.execNS, k.execSelector, k.execCommand = namespace, selector, command
	k.execs = append(k.execs, command)
	return k.err
}

func (k *recordKube) Patch(_ context.Context, group, version, resource, namespace, name string, mergePatch []byte) error {
	k.patchGVR = [3]string{group, version, resource}
	k.patchNS, k.patchName, k.patchBody = namespace, name, string(mergePatch)
	return k.patchErr
}

func (k *recordKube) GetConfigMapKey(_ context.Context, namespace, name, key string) (string, error) {
	k.getNS, k.getName, k.getKey = namespace, name, key
	if k.err != nil {
		return "", k.err
	}
	if k.getReturn != "" {
		return k.getReturn, nil
	}
	return `{"flags":{"productCatalogFailure":{"defaultVariant":"on"},"cartFailure":{"defaultVariant":"on"}}}`, nil
}

func TestRunner_DispatchesExactExecForHoldRebalance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		reverse bool
		want    []string
	}{
		{"forward sets noout", false, []string{"ceph", "osd", "set", "noout"}},
		{"reverse unsets noout", true, []string{"ceph", "osd", "unset", "noout"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k := &recordKube{}
			r, err := actuate.NewWithForgeForTest(k, &recordForge{}, configtest.ShippedCatalog(t))
			if err != nil {
				t.Fatalf("build runner from shipped catalog: %v", err)
			}

			if _, err := r.Run(context.Background(), "hold-rebalance", tc.reverse, nil, "dummy notes"); err != nil {
				t.Fatalf("Run(hold-rebalance) returned error: %v", err)
			}
			if k.execNS != "rook-ceph" || k.execSelector != "app=rook-ceph-tools" {
				t.Errorf("exec targeted %s/%q, want rook-ceph/app=rook-ceph-tools", k.execNS, k.execSelector)
			}
			if diff := cmp.Diff(tc.want, k.execCommand); diff != "" {
				t.Errorf("exec argv drifted (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunner_UnboundRefIsAnError(t *testing.T) {
	t.Parallel()
	r, err := actuate.NewWithForgeForTest(&recordKube{}, &recordForge{}, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatalf("build runner from shipped catalog: %v", err)
	}
	_, err = r.Run(context.Background(), "no-such-action", false, nil, "dummy notes")
	if err == nil {
		t.Fatal("an unbound ref must error, not silently no-op")
	}
}

func TestRunner_PropagatesKubeFailure(t *testing.T) {
	t.Parallel()
	r, err := actuate.NewWithForgeForTest(&recordKube{err: errors.New("connection refused")}, &recordForge{}, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatalf("build runner from shipped catalog: %v", err)
	}
	if _, err := r.Run(context.Background(), "hold-rebalance", false, nil, "dummy notes"); err == nil {
		t.Fatal("a failing mutation must surface as an error")
	}
}

func TestRunner_DispatchesFlagVariantPatchForDisableProductCatalogFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		reverse     bool
		wantVariant string
	}{
		{"forward disables the flag", false, "off"},
		{"reverse re-arms the flag", true, "on"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k := &recordKube{}
			r, err := actuate.NewWithForgeForTest(k, &recordForge{}, configtest.ShippedCatalog(t))
			if err != nil {
				t.Fatalf("build runner from shipped catalog: %v", err)
			}

			if _, err := r.Run(context.Background(), "disable-product-catalog-failure", tc.reverse, nil, "dummy notes"); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			if k.getNS != "otel-demo" || k.getName != "flagd-config" || k.getKey != "demo.flagd.json" {
				t.Errorf("read %s/%s[%s], want otel-demo/flagd-config[demo.flagd.json]", k.getNS, k.getName, k.getKey)
			}
			wantGVR := [3]string{"", "v1", "configmaps"}
			if k.patchGVR != wantGVR || k.patchNS != "otel-demo" || k.patchName != "flagd-config" {
				t.Errorf("patched %v %s/%s, want %v otel-demo/flagd-config", k.patchGVR, k.patchNS, k.patchName, wantGVR)
			}

			var patch struct {
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal([]byte(k.patchBody), &patch); err != nil {
				t.Fatalf("patch body isn't valid JSON: %v\nbody: %s", err, k.patchBody)
			}
			var blob struct {
				Flags map[string]struct {
					DefaultVariant string `json:"defaultVariant"`
				} `json:"flags"`
			}
			if err := json.Unmarshal([]byte(patch.Data["demo.flagd.json"]), &blob); err != nil {
				t.Fatalf("patched demo.flagd.json isn't valid JSON: %v", err)
			}
			if got := blob.Flags["productCatalogFailure"].DefaultVariant; got != tc.wantVariant {
				t.Errorf("productCatalogFailure.defaultVariant = %q, want %q", got, tc.wantVariant)
			}
			// The read-modify-write must leave every other flag untouched —
			// cartFailure's fixture value is "on" and this op never names it.
			if got := blob.Flags["cartFailure"].DefaultVariant; got != "on" {
				t.Errorf("untouched flag cartFailure.defaultVariant drifted to %q, want on", got)
			}
		})
	}
}

func TestRunner_DispatchesDeploymentPatchForRestartCartPod(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		k := &recordKube{}
		r, err := actuate.NewWithForgeForTest(k, &recordForge{}, configtest.ShippedCatalog(t))
		if err != nil {
			t.Fatalf("build runner from shipped catalog: %v", err)
		}

		if _, err := r.Run(context.Background(), "restart-cart-pod", reverse, nil, "dummy notes"); err != nil {
			t.Fatalf("Run(restart-cart-pod, reverse=%v) returned error: %v", reverse, err)
		}

		wantGVR := [3]string{"apps", "v1", "deployments"}
		if k.patchGVR != wantGVR || k.patchNS != "otel-demo" || k.patchName != "cart" {
			t.Errorf("patched %v %s/%s, want %v otel-demo/cart", k.patchGVR, k.patchNS, k.patchName, wantGVR)
		}

		var patch struct {
			Spec struct {
				Template struct {
					Metadata struct {
						Annotations map[string]string `json:"annotations"`
					} `json:"metadata"`
				} `json:"template"`
			} `json:"spec"`
		}
		if err := json.Unmarshal([]byte(k.patchBody), &patch); err != nil {
			t.Fatalf("patch body isn't valid JSON: %v\nbody: %s", err, k.patchBody)
		}
		if _, ok := patch.Spec.Template.Metadata.Annotations["thump.io/restartedAt"]; !ok {
			t.Errorf("patch body missing thump.io/restartedAt annotation: %s", k.patchBody)
		}
	}
}

func TestRunner_FlagVariantOp_UnknownFlagIsAnError(t *testing.T) {
	t.Parallel()
	k := &recordKube{getReturn: `{"flags":{"someOtherFlag":{"defaultVariant":"on"}}}`}
	r, err := actuate.NewWithForgeForTest(k, &recordForge{}, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatalf("build runner from shipped catalog: %v", err)
	}

	_, err = r.Run(context.Background(), "disable-cart-failure", false, nil, "dummy notes")
	if err == nil {
		t.Fatal("a flagd blob missing the target flag must error, not silently patch")
	}
	if k.patchName != "" {
		t.Error("must not patch when the target flag isn't in the blob")
	}
}

func TestNew_RefusesRatherThanHalfBuildingARunnerOffCluster(t *testing.T) {
	if _, err := actuate.New(configtest.ShippedCatalog(t)); !errors.Is(err, rest.ErrNotInCluster) {
		t.Errorf("New must refuse without in-cluster config, got %v", err)
	}
}

func TestRunner_DispatchesEveryStepOfAMultiStepForwardInAuthoredOrder(t *testing.T) {
	t.Parallel()
	k := &recordKube{}
	r, err := actuate.NewWithForgeForTest(k, &recordForge{}, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatalf("build runner from shipped catalog: %v", err)
	}

	if _, err := r.Run(context.Background(), "accelerate-recovery", false, nil, "dummy notes"); err != nil {
		t.Fatal(err)
	}

	wantGVR := [3]string{"apps", "v1", "deployments"}
	if k.patchGVR != wantGVR || k.patchNS != "rook-ceph" || k.patchName != "rook-ceph-operator" {
		t.Errorf("leading step patched %v %s/%s, want %v rook-ceph/rook-ceph-operator", k.patchGVR, k.patchNS, k.patchName, wantGVR)
	}
	if k.patchBody != `{"spec":{"replicas":0}}` {
		t.Errorf("leading step scaled to %q, want the operator paused at zero replicas", k.patchBody)
	}
	want := [][]string{
		{"ceph", "config", "set", "osd", "osd_mclock_override_recovery_settings", "true"},
		{"ceph", "config", "set", "osd", "osd_max_backfills", "16"},
		{"ceph", "config", "set", "osd", "osd_recovery_max_active", "16"},
	}
	if diff := cmp.Diff(want, k.execs); diff != "" {
		t.Error("multi-step forward drifted from the authored step list", diff)
	}
}

func TestRunner_AMultiStepForwardStopsAtTheFirstFailingStep(t *testing.T) {
	t.Parallel()
	k := &recordKube{err: errors.New("connection refused")}
	r, err := actuate.NewWithForgeForTest(k, &recordForge{}, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatalf("build runner from shipped catalog: %v", err)
	}

	if _, err := r.Run(context.Background(), "accelerate-recovery", false, nil, "dummy notes"); err == nil {
		t.Fatal("a failing step must surface as an error")
	}
	if k.patchName == "" {
		t.Error("the leading scale step must still run before the failing exec step")
	}
	if len(k.execs) != 1 {
		t.Errorf("ran %d exec steps after the first failed, want 1", len(k.execs))
	}
}

// hangingKube.Exec never returns on its own — it blocks on ctx, the way a
// client-go call against a wedged apiserver connection does with no client
// Config.Timeout set. It stands in for that gap without needing a real
// apiserver to wedge.
type hangingKube struct{ recordKube }

func (k *hangingKube) Exec(ctx context.Context, _, _ string, _ []string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRunner_TimesOutAHungMutationRatherThanBlockingForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, err := actuate.NewWithTimeoutForTest(&hangingKube{}, configtest.ShippedCatalog(t), 5*time.Second, &recordForge{})
		if err != nil {
			t.Fatalf("build runner from shipped catalog: %v", err)
		}

		_, err = r.Run(t.Context(), "hold-rebalance", false, nil, "dummy notes")

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Run(hung exec) = %v, want a deadline error", err)
		}
	})
}

// TestRunner_CutsOneReleasePerContractRefWhenADecisionIsRedelivered pins the
// idempotency key to the authored ref. A JetStream consumer redelivers on any
// missed ack, and an op that cut a fresh release per delivery would turn one
// governed decision into a queue of individually-acceptable releases against
// the same file.
func TestRunner_CutsOneReleasePerContractRefWhenADecisionIsRedelivered(t *testing.T) {
	t.Parallel()

	f := &recordForge{doc: `{"flags":{"cartFailure":{"defaultVariant":"on"}}}`}
	r, err := actuate.NewWithForgeForTest(&recordKube{}, f, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, err := r.Run(t.Context(), "disable-cart-failure-release", false, nil, "dummy notes"); err != nil {
			t.Fatal(err)
		}
	}

	if diff := cmp.Diff([]string{"disable-cart-failure-release"}, f.keys); diff != "" {
		t.Error("wrong release keys cut for one redelivered decision", diff)
	}
}

// TestNewWith_RefusesAReleaseContractWhenNoForgeIsWired pins the load-time
// refusal. A contract naming a delivery pipeline the process cannot reach is
// a startup error, never a runtime failure discovered the first time
// governance approves it.
func TestNewWith_RefusesAReleaseContractWhenNoForgeIsWired(t *testing.T) {
	t.Parallel()

	_, err := actuate.NewWithForgeForTest(&recordKube{}, nil, configtest.ShippedCatalog(t))

	if !errors.Is(err, actuate.ErrUnbindable) {
		t.Fatalf("want ErrUnbindable for a release contract with no forge, got %v", err)
	}
}

// TestRunner_LeavesEveryOtherFlagAloneWhenCuttingARelease pins the
// read-modify-write against the source of record. The flagd document is one
// opaque blob holding every flag, so a release that rewrote only its own key
// would disarm every other flag in the same commit.
func TestRunner_LeavesEveryOtherFlagAloneWhenCuttingARelease(t *testing.T) {
	t.Parallel()

	f := &recordForge{doc: `{"flags":{"cartFailure":{"defaultVariant":"on"},"adServiceFailure":{"defaultVariant":"on"}}}`}
	r, err := actuate.NewWithForgeForTest(&recordKube{}, f, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.Run(t.Context(), "disable-cart-failure-release", false, nil, "dummy notes"); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Flags map[string]struct {
			DefaultVariant string `json:"defaultVariant"`
		} `json:"flags"`
	}
	if err := json.Unmarshal(f.content, &got); err != nil {
		t.Fatalf("released document isn't valid JSON: %v\ndocument: %s", err, f.content)
	}
	if diff := cmp.Diff("off", got.Flags["cartFailure"].DefaultVariant); diff != "" {
		t.Error("wrong variant for the flag under release", diff)
	}
	if diff := cmp.Diff("on", got.Flags["adServiceFailure"].DefaultVariant); diff != "" {
		t.Error("an untouched flag drifted in the released document", diff)
	}
}

// TestRunner_CutsTheRevertAsItsOwnReleaseRatherThanRewritingTheForwardOne
// pins the two directions to two keys. Forward and reverse both cut against
// the same authored path, so a shared key makes the undo update the open
// forward release in place — the reviewer sees one release that now says
// nothing changed, and the change it was meant to undo is still live.
func TestRunner_CutsTheRevertAsItsOwnReleaseRatherThanRewritingTheForwardOne(t *testing.T) {
	t.Parallel()

	f := &recordForge{doc: `{"flags":{"cartFailure":{"defaultVariant":"on"}}}`}
	r, err := actuate.NewWithForgeForTest(&recordKube{}, f, configtest.ShippedCatalog(t))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.Run(t.Context(), "disable-cart-failure-release", false, nil, "dummy notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(t.Context(), "disable-cart-failure-release", true, nil, "dummy notes"); err != nil {
		t.Fatal(err)
	}

	want := []string{"disable-cart-failure-release", "disable-cart-failure-release:revert"}
	if diff := cmp.Diff(want, f.keys); diff != "" {
		t.Error("wrong release keys for a forward followed by its undo", diff)
	}
}

// recordForge is a fake actuate.Forge: it records every release cut so a test
// can assert the exact artifact, and never reaches a real forge. doc is the
// canned source-of-record document Read hands back.
type recordForge struct {
	doc     string
	err     error
	keys    []string // every distinct Release.Key cut, in order
	content []byte   // the most recent released document
	notes   string
}

func (f *recordForge) Read(context.Context, string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.doc), nil
}

func (f *recordForge) Cut(_ context.Context, rel forge.Release) (string, error) {
	if !slices.Contains(f.keys, rel.Key) {
		f.keys = append(f.keys, rel.Key)
	}
	f.content, f.notes = rel.Content, rel.Notes
	return "https://forge.example/release/1", f.err
}

func (f *recordForge) Withdraw(context.Context, string) (bool, error) { return false, f.err }
