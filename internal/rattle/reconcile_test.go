package rattle_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/rattle"
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

func TestReconcile_EmitsOnCorrelationEvenWithoutAcceleration(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1"}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 3, 4)})
	r.Correlation = &rattle.CorrelationDetector{MinSignals: 2}
	r.CorrelationSource = fakeMultiSignalSource{slo.ID: multiSignal(map[string][]float64{
		"retries": {1, 2, 3, 4}, "timeouts": {1, 2, 3, 4},
	})}

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Error("correlation alone should have fired Reconcile", cmp.Diff(1, len(got)))
	}
}

func TestReconcile_EmitsOnEnvelopeBreachEvenWithoutAcceleration(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1"}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 3, 4)})
	r.Envelope = &rattle.EnvelopeDetector{K: 2}
	r.BaselineSource = fakeBaselineSource{slo.ID: window(1, 1.1, 0.9, 1.0, 1.05)}

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Error("envelope breach alone should have fired Reconcile", cmp.Diff(1, len(got)))
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

type fakeMultiSignalSource map[string]rattle.MultiSignalWindow

func (f fakeMultiSignalSource) MultiSignals(_ context.Context, slo rattle.SLO) (rattle.MultiSignalWindow, error) {
	return f[slo.ID], nil
}

type fakeBaselineSource map[string][]rattle.Sample

func (f fakeBaselineSource) BaselineSamples(_ context.Context, slo rattle.SLO) ([]rattle.Sample, error) {
	return f[slo.ID], nil
}

func TestReconcile_SkipsAStaleWindowWhenContractSet(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1"}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)}) // would fire
	r.Contract = &rattle.SignalContract{FreshnessBound: time.Minute}                  // frozen clock is far newer than this

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Error("a stale window under contract must not fire, even though the detector would have", cmp.Diff(0, len(got)))
	}
}

func TestReconcile_AttenuatesConfidenceInsideAnExclusionWindow(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-rgw-availability", Object: "ceph-rgw", Tier: "tier-1"}
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)})
	r.Contract = &rattle.SignalContract{
		FreshnessBound:   time.Hour,
		ConfidenceFloor:  0.1,
		ExclusionWindows: []rattle.ExclusionWindow{{Start: time.Unix(0, 0), End: time.Unix(2000, 0)}},
	}

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatal("expected the detector to still fire, just at lowered confidence")
	}
	if diff := cmp.Diff(0.5, got[0].Divergence.Confidence); diff != "" {
		t.Error("wrong attenuated confidence on the emitted Detection", diff)
	}
}

func TestReconcile_EnrichesWhenSourcesPresentZeroValueWhenNil(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "x", Object: "ceph-rgw", Tier: "tier-1", Dependencies: []rattle.Dependency{{Name: "payment-gateway"}}}

	withSources := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)})
	withSources.TopologySource = fakeTopologySource{"payment-gateway": "degraded"}
	withSources.TrafficSource = fakeTrafficSource{{AffectedPct: 0.4}}

	got, _ := withSources.Reconcile(context.Background())
	if len(got[0].Topology.Upstream) == 0 {
		t.Error("TopologySource set but Detection.Topology.Upstream still empty — wiring didn't fire")
	}
	if got[0].Traffic.AffectedPct == 0 {
		t.Error("TrafficSource set but Detection.Traffic still zero-value")
	}

	withoutSources := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)})
	got2, _ := withoutSources.Reconcile(context.Background())
	if got2[0].Topology.Upstream != nil {
		t.Error("no TopologySource set — must reproduce zero-value Topology, same as pre-W8")
	}
}

type fakeTrafficSource []rattle.TrafficSample

func (f fakeTrafficSource) TrafficSamples(_ context.Context, _ rattle.SLO) ([]rattle.TrafficSample, error) {
	return f, nil
}

func TestReconcile_GoldenPath_EmitsOneFullyEnrichedDetection(t *testing.T) {
	t.Parallel()
	slo := goldenSLO() // tier-1 ceph-rgw with declared Dependencies, so enrichment has real inputs
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1, 2, 4, 8)})
	r.Contract = &rattle.SignalContract{FreshnessBound: time.Hour, ConfidenceFloor: 0.1}
	r.TopologySource = fakeTopologySource{"ceph-osd": "degrade"}
	r.TrafficSource = fakeTrafficSource(trafficWindow(0.1, 0.2, 0.4))

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal("golden path must not error", err)
	}
	if len(got) != 1 {
		t.Fatalf("golden path emits exactly one detection, got %d", len(got))
	}
	if diff := cmp.Diff(goldenDetection(), got[0],
		cmpopts.IgnoreFields(signal.Detection{}, "DetectedAt")); diff != "" {
		t.Error("enriched detection drifted from the golden fixture", diff)
	}
}

func goldenSLO() rattle.SLO {
	return rattle.SLO{
		ID:           "ceph-rgw-availability",
		Object:       "ceph-rgw",
		Tier:         "tier-1",
		Objective:    0.999,
		ContractRef:  "ceph-rgw-availability:v1",
		Dependencies: []rattle.Dependency{{Name: "ceph-osd", Role: "blocking"}},
	}
}

func goldenDetection() signal.Detection {
	return signal.Detection{
		Name:          "ceph-rgw-burn-accel",      // SignalFor: env.AffectedObject() + "-burn-accel"
		Fingerprint:   "slo_burn:ceph-rgw",        // fingerprint(env): Kind() + ":" + Object
		OriginService: "ceph-rgw",                 // SLO.Object
		ServiceTier:   "tier-1",                   // SLO.Tier
		DetectorType:  "burn_rate_acceleration",   // the acceleration branch fired
		ContractRef:   "ceph-rgw-availability:v1", // SLO.ContractRef
		Divergence: signal.Divergence{
			Observed:   1.5,            // Detect(window(1,2,4,8)): mean(2nd diffs) = mean([1,2])
			Confidence: 1.0,            // Contract.Attenuated(1.0, now): no exclusion window, floor 0.1
			Trajectory: "accelerating", // SignalFor hardcodes this for the accel detector
		},
		Topology: signal.TopologyContext{
			Upstream: []signal.ObservedNode{{Service: "ceph-osd", State: "degrade"}}, // EnrichTopology
		},
		Traffic: signal.TrafficContext{AffectedPct: 0.4}, // EnrichTraffic: last trafficWindow point
		Impact: signal.Impact{
			Severity: signal.Severity{
				Trajectory:     "accelerating",
				DegradationPct: 0.008000000000000007, // EnrichSeverity: degration pct off last sample,
			},
			BlastRadius: signal.BlastRadius{AffectedPct: 0.4, Velocity: "accelerating"}, // EnrichTraffic: last pct + trajectory of the TRAFFIC window
		},
		// DetectedAt would be time.Unix(1000,0) (the frozen clock) — IGNORED via cmpopts, see below.
	}
}

func TestReconcile_EmitsOnSustainedBurnEvenWithoutAcceleration(t *testing.T) {
	t.Parallel()
	slo := rattle.SLO{ID: "ceph-health", Object: "ceph-cluster", Tier: "tier-1"}

	// Create a reconciler with Sustained detector configured
	r := newTestReconciler([]rattle.SLO{slo}, fakeSource{slo.ID: window(1000, 1000, 1000, 1000, 1000)})
	r.Sustained = &rattle.SustainedBurnDetector{Threshold: 1.0, MinSamples: 5}

	got, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 detection, got %d", len(got))
	}
	if got[0].DetectorType != "sustained_burn" {
		t.Errorf("expected detector type 'sustained_burn', got %q", got[0].DetectorType)
	}
	if got[0].Divergence.Trajectory != "stable" {
		t.Errorf("expected trajectory to be 'stable', got %q", got[0].Divergence.Trajectory)
	}
}
