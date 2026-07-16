package actuate_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/actuate"
)

func TestRunner_DispatchesExactArgvForEachBinding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		ref     string
		reverse bool
		want    []string
	}{
		{
			name: "hold-rebalance forward sets noout",
			ref:  "hold-rebalance", reverse: false,
			want: []string{"kubectl", "-n", "rook-ceph", "exec", "deploy/rook-ceph-tools", "--", "ceph", "osd", "set", "noout"},
		},
		{
			name: "hold-rebalance reverse unsets noout",
			ref:  "hold-rebalance", reverse: true,
			want: []string{"kubectl", "-n", "rook-ceph", "exec", "deploy/rook-ceph-tools", "--", "ceph", "osd", "unset", "noout"},
		},
		{
			name: "scale-out forward patches instances up",
			ref:  "scale-out-rgw-gateways", reverse: false,
			want: []string{"kubectl", "-n", "rook-ceph", "patch", "cephobjectstore", "<store>", "--type", "merge", "-p", `{"spec":{"gateway":{"instances":2}}}`},
		},
		{
			name: "scale-out reverse patches instances back",
			ref:  "scale-out-rgw-gateways", reverse: true,
			want: []string{"kubectl", "-n", "rook-ceph", "patch", "cephobjectstore", "<store>", "--type", "merge", "-p", `{"spec":{"gateway":{"instances":1}}}`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			fake := func(_ context.Context, name string, args ...string) ([]byte, error) {
				got = append([]string{name}, args...)
				return nil, nil
			}
			r := actuate.NewWith(fake)

			if err := r.Run(context.Background(), tc.ref, tc.reverse, nil); err != nil {
				t.Fatalf("Run(%s) returned error: %v", tc.ref, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("argv drifted (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunner_UnboundRefIsAnError(t *testing.T) {
	t.Parallel()
	r := actuate.NewWith(func(context.Context, string, ...string) ([]byte, error) { return nil, nil })
	err := r.Run(context.Background(), "throttle-non-critical-paths", false, nil)
	if err == nil {
		t.Fatal("an unbound ref must error, not silently no-op")
	}
}

func TestRunner_PropagatesCommandFailure(t *testing.T) {
	t.Parallel()
	boom := func(context.Context, string, ...string) ([]byte, error) {
		return []byte("connection refused"), context.DeadlineExceeded
	}
	r := actuate.NewWith(boom)
	if err := r.Run(context.Background(), "hold-rebalance", false, nil); err == nil {
		t.Fatal("a failing command must surface as an error")
	}
}
