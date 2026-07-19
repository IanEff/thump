package actuate_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
)

// recordKube is a fake actuate.Kube: it records the single request it was
// asked to make (exec, patch, or configmap read) so a test can assert the
// exact mutation each binding dispatches, and never touches an apiserver.
// err, when set, is returned so the failure-propagation test can force a
// failure.
type recordKube struct {
	err error

	// exec
	execNS       string
	execSelector string
	execCommand  []string

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
	return k.err
}

func (k *recordKube) Patch(_ context.Context, group, version, resource, namespace, name string, mergePatch []byte) error {
	k.patchGVR = [3]string{group, version, resource}
	k.patchNS, k.patchName, k.patchBody = namespace, name, string(mergePatch)
	return k.err
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
			r := actuate.NewWith(k)

			if err := r.Run(context.Background(), "hold-rebalance", tc.reverse, nil); err != nil {
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
	r := actuate.NewWith(&recordKube{})
	err := r.Run(context.Background(), "no-such-action", false, nil)
	if err == nil {
		t.Fatal("an unbound ref must error, not silently no-op")
	}
}

func TestRunner_PropagatesKubeFailure(t *testing.T) {
	t.Parallel()
	r := actuate.NewWith(&recordKube{err: errors.New("connection refused")})
	if err := r.Run(context.Background(), "hold-rebalance", false, nil); err == nil {
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
			r := actuate.NewWith(k)

			if err := r.Run(context.Background(), "disable-product-catalog-failure", tc.reverse, nil); err != nil {
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

func TestRunner_FlagVariantOp_UnknownFlagIsAnError(t *testing.T) {
	t.Parallel()
	k := &recordKube{getReturn: `{"flags":{"someOtherFlag":{"defaultVariant":"on"}}}`}
	r := actuate.NewWith(k)

	err := r.Run(context.Background(), "disable-cart-failure", false, nil)
	if err == nil {
		t.Fatal("a flagd blob missing the target flag must error, not silently patch")
	}
	if k.patchName != "" {
		t.Error("must not patch when the target flag isn't in the blob")
	}
}
