package actuate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
)

// recordKube is a fake actuate.Kube: it records the single request it was
// asked to make (exec or patch) so a test can assert the exact mutation each
// binding dispatches, and never touches an apiserver. err, when set, is
// returned so the failure-propagation test can force a failure.
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

func TestRunner_DispatchesExactPatchForScaleOut(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		reverse bool
		want    string
	}{
		{"forward patches instances up", false, `{"spec":{"gateway":{"instances":2}}}`},
		{"reverse patches instances back", true, `{"spec":{"gateway":{"instances":1}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k := &recordKube{}
			r := actuate.NewWith(k)

			if err := r.Run(context.Background(), "scale-out-rgw-gateways", tc.reverse, nil); err != nil {
				t.Fatalf("Run(scale-out-rgw-gateways) returned error: %v", err)
			}
			if k.patchGVR != [3]string{"ceph.rook.io", "v1", "cephobjectstores"} {
				t.Errorf("patch GVR = %v, want ceph.rook.io/v1/cephobjectstores", k.patchGVR)
			}
			if k.patchNS != "rook-ceph" || k.patchName != "ceph-objectstore" {
				t.Errorf("patch targeted %s/%q, want rook-ceph/ceph-objectstore", k.patchNS, k.patchName)
			}
			if diff := cmp.Diff(tc.want, k.patchBody); diff != "" {
				t.Errorf("patch body drifted (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunner_UnboundRefIsAnError(t *testing.T) {
	t.Parallel()
	r := actuate.NewWith(&recordKube{})
	err := r.Run(context.Background(), "throttle-non-critical-paths", false, nil)
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
