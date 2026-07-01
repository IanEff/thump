package clank_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/clank/internal/clank"
	"github.com/ianeff/clank/internal/rattle"
	"github.com/ianeff/clank/internal/signal"
)

// seamSource is a three-line in-process rattle.Source (the rattle test fakes live in
// package rattle_test and can't be imported here, so we spell it out).
type seamSource struct{ samples []rattle.Sample }

func (s seamSource) BurnSamples(context.Context, rattle.SLO) ([]rattle.Sample, error) {
	return s.samples, nil
}

// seamDetection runs a REAL rattle.Reconcile and returns its single Detection — the
// actual seam output, not a hand-built signal.Detection.
func seamDetection(t *testing.T) signal.Detection {
	t.Helper()
	base := time.Unix(0, 0)
	burn := []rattle.Sample{ // 1,2,4,8 → accelerating → fires the acceleration detector
		{T: base, BurnRate: 1},
		{T: base.Add(time.Minute), BurnRate: 2},
		{T: base.Add(2 * time.Minute), BurnRate: 4},
		{T: base.Add(3 * time.Minute), BurnRate: 8},
	}
	r := &rattle.Reconciler{
		SLOs: []rattle.SLO{{
			ID: "ceph-rgw-availability", Object: "ceph-rgw",
			Tier: "tier-1", ContractRef: "ceph-rgw-availability:v1", // Tier MUST match the catalog's ApplicableTiers
		}},
		Source:   seamSource{samples: burn},
		Detector: rattle.AccelerationDetector{Threshold: 0.5},
		Debounce: rattle.NewDebouncer(10 * time.Minute),
		Now:      func() time.Time { return time.Unix(1000, 0) },
	}
	dets, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("seam precondition: rattle.Reconcile errored: %v", err)
	}
	if len(dets) != 1 {
		t.Fatalf("seam precondition: want exactly 1 detection, got %d", len(dets))
	}
	return dets[0] // Fingerprint "slo_burn:ceph-rgw", ServiceTier "tier-1"
}

func TestSeam_RattleDetectionDrivesClankToADeliveredProposal(t *testing.T) {
	t.Parallel()
	det := seamDetection(t) // real rattle output

	// scripted clank model: step 1 investigate (live evidence → clears the gate),
	// step 2 propose a CATALOGUED action for the detection's class + tier.
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation, // must be in newTestEngine's catalog
			Hypotheses:   []clank.Hypothesis{{Name: "rgw_pool_saturation", Weight: 0.8}},
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}

	eng, sink := newTestEngine(model) // the EXACT engine the clank tests use
	set, err := eng.Propose(context.Background(), det)
	if err != nil {
		t.Fatal("clank must accept a real rattle Detection without error", err)
	}

	// the seam held: gated through, delivered once, and the fingerprint survived intact.
	if set.Gate == nil || !set.Gate.Passed {
		t.Errorf("gate rejected a well-formed seam detection: %+v", set.Gate)
	}
	if diff := cmp.Diff("proposed", set.Status.Phase); diff != "" {
		t.Error("delivered proposal should be phase=proposed (-want +got)", diff)
	}
	if len(sink.delivered) != 1 {
		t.Fatalf("a passed set is delivered exactly once; delivered %d", len(sink.delivered))
	}
	if diff := cmp.Diff("slo_burn:ceph-rgw", sink.delivered[0].SignalRef); diff != "" {
		t.Error("fingerprint didn't survive the seam (ProposalSet.SignalRef) (-want +got)", diff)
	}
}
