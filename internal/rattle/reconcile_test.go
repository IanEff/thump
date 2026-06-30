package rattle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/rattle"
)

func TestReconcile(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999}
	cases := map[string]struct {
		samples []rattle.Sample
		wantLen int
	}{
		"Reconcile emits one Detection for an accelerating SLO": {window(1, 2, 4, 8), 1},
		"Reconcile stays silent for a steady (non-accel) climb": {window(1, 2, 3, 4), 0}, // earns its keep
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: tc.samples})
			got, err := r.Reconcile(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.wantLen {
				t.Error("wrong number of Detections emitted", cmp.Diff(tc.wantLen, len(got)))
			}
		})
	}
}

func TestReconcile_SuppressesADuplicateAcrossPasses(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1", Objective: 0.999}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)})

	first, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 0 {
		t.Errorf("same firing across two passes should emit once: got %d, then %d", len(first), len(second))
	}
}

func newTestReconciler(slos []rattle.SLO, src rattle.Source) *rattle.Reconciler {
	frozen := time.Unix(1000, 0)
	return &rattle.Reconciler{
		SLOs:     slos,
		Source:   src,
		Detector: rattle.AccelerationDetector{Threshold: 0.5},
		Debounce: rattle.NewDebouncer(10 * time.Minute),
		Now:      func() time.Time { return frozen },
	}
}

type fakeSource map[string][]rattle.Sample

func (f fakeSource) BurnSamples(_ context.Context, slo rattle.SLO) ([]rattle.Sample, error) {
	return f[slo.ID], nil
}
