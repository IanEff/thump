//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
)

// recordingTool is a read-only telemetry tool that counts its invocations, so a
// test can prove the loop actually investigated before proposing.
type recordingTool struct {
	spec  clank.ToolSpec
	ref   clank.EvidenceRef
	calls int
}

func (r *recordingTool) Spec() clank.ToolSpec { return r.spec }
func (r *recordingTool) Run(_ context.Context, _ json.RawMessage) (clank.EvidenceRef, error) {
	r.calls++
	return r.ref, nil
}

type staticTopo struct{ snap clank.TopologySnapshot }

func (s staticTopo) Topology(_ context.Context, _ signal.Detection) (clank.TopologySnapshot, error) {
	return s.snap, nil
}

type staticChange struct{ snap clank.ChangeSnapshot }

func (s staticChange) Changes(_ context.Context, _ signal.Detection) (clank.ChangeSnapshot, error) {
	return s.snap, nil
}

type capturePublisher struct{ delivered []clank.ProposalSet }

func (c *capturePublisher) Publish(_ context.Context, _ string, ps clank.ProposalSet) error {
	c.delivered = append(c.delivered, ps)
	return nil
}

// newLiveEngine wires the full engine with the REAL model and fake everything else.
func newLiveEngine(t *testing.T, tool clank.Tool, catalog *clank.StaticCatalog) (*clank.Engine, *capturePublisher) {
	t.Helper()
	pub := &capturePublisher{}
	return &clank.Engine{
		Intake: clank.NewIntake(
			staticTopo{clank.TopologySnapshot{Downstream: []clank.NodeState{
				{Name: "payments-db", State: "degraded", TrafficShare: 0.7},
			}}},
			staticChange{clank.ChangeSnapshot{Events: []clank.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model:        clank.NewAnthropicModel(apiKey(t)),
		Tools:        map[string]clank.Tool{tool.Spec().Name: tool},
		Catalog:      catalog,
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		Ledger:       clank.NewMemProposalLog(),
		Pub:          pub,
		MaxSteps:     8,
	}, pub
}

func goldenSignal() signal.Detection {
	return signal.Detection{
		Name:        "checkout-latency-burn-accel-001",
		Fingerprint: "fp-checkout-latency-001",
		ServiceTier: "tier-1",
		Divergence:  signal.Divergence{Metric: "latency_p99", Observed: 850, Baseline: 200, Confidence: 0.9, Trajectory: "accelerating"},
		Impact: signal.Impact{
			Severity:    signal.Severity{DegradationPct: 40, Trajectory: "accelerating"},
			BlastRadius: signal.BlastRadius{AffectedPct: 60, Velocity: "fast", DownstreamConsumers: 3},
		},
		DetectedAt: time.Now(),
	}
}

func TestEngine_GoldenPath_SignalToDeliveredProposalSet(t *testing.T) {
	metrics := &recordingTool{
		spec: clank.ToolSpec{Name: "metrics", Description: "read-only telemetry query for a service's live metrics"},
		ref:  clank.EvidenceRef{Tool: "metrics", Summary: "payments-db CPU pinned at 99%, connection pool exhausted", Ref: "metrics://payments-db/cpu", Live: true},
	}
	// Broadly applicable on purpose: this test exercises the LOOP, not Haiku's
	// taste in failure-class labels. Whatever class it picks, the action stays
	// in-catalog, so the test fails only for real wiring reasons.
	catalog := clank.NewStaticCatalog([]clank.ActionContract{{
		Name: "throttle-non-critical-paths",
		ApplicableFailureClasses: []clank.FailureClass{
			clank.ClassDependencySaturation, clank.ClassResourceExhaustion,
			clank.ClassTrafficShift, clank.ClassUnknown,
		},
		ApplicableTiers: []string{"tier-1"},
	}})

	e, sink := newLiveEngine(t, metrics, catalog)
	set, err := e.Propose(callCtx(t), goldenSignal())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	// The loop investigated before it proposed — the gate's live-evidence floor
	// (anyLive) depends on it, and so does honest reasoning.
	if metrics.calls == 0 {
		t.Error("model proposed without calling the telemetry tool; the loop didn't investigate")
	}
	if set.Status.Phase != "proposed" {
		t.Fatalf("golden path should reach phase \"proposed\"; got %q (gate %+v)", set.Status.Phase, set.Gate)
	}
	if set.Gate == nil || !set.Gate.Passed {
		t.Errorf("a grounded, deduped, in-budget set must pass the gate: %+v", set.Gate)
	}
	if len(set.Proposals) == 0 {
		t.Fatal("a passed set must carry at least one proposal")
	}
	// Autonomy boundary, behavioural, against the REAL model.
	for _, c := range set.Proposals {
		if c.ContractRef != "throttle-non-critical-paths" {
			t.Errorf("proposed an action outside the catalog: %q", c.ContractRef)
		}
	}
	if set.Recommended != set.Proposals[0].ID {
		t.Errorf("recommended must be the rank-1 proposal: rec=%q rank1=%q", set.Recommended, set.Proposals[0].ID)
	}
	if set.SAOSnapshot == nil || set.SAOSnapshot.Version == 0 {
		t.Error("the SAO the loop reasoned over must be frozen onto the set")
	}
	if len(sink.delivered) != 1 {
		t.Errorf("a passed set is delivered exactly once; delivered %d", len(sink.delivered))
	}
}

func TestEngine_ThinEvidence_YieldsNoActionAndDeliversNothing(t *testing.T) {
	metrics := &recordingTool{
		spec: clank.ToolSpec{Name: "metrics", Description: "read-only telemetry query for a service's live metrics"},
		ref:  clank.EvidenceRef{Tool: "metrics", Summary: "all services nominal; no anomaly on payments-db", Ref: "metrics://payments-db/cpu", Live: true},
	}
	catalog := clank.NewStaticCatalog([]clank.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []clank.FailureClass{clank.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
	}})

	e, sink := newLiveEngine(t, metrics, catalog)
	set, err := e.Propose(callCtx(t), goldenSignal())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if set.Status.Phase == "proposed" {
		t.Errorf("evidence saying \"all nominal\" should not reach a proposal: %+v", set)
	}
	if len(sink.delivered) != 0 {
		t.Errorf("a non-proposed set must deliver nothing; delivered %d", len(sink.delivered))
	}
}
