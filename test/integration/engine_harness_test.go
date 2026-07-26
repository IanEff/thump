package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish/publishtest"
)

// recordingTool is a read-only telemetry tool that counts its invocations, so a
// test can prove the loop actually investigated before proposing.
type recordingTool struct {
	spec  clank.ToolSpec
	ref   proposal.EvidenceRef
	calls int
}

func (r *recordingTool) Spec() clank.ToolSpec { return r.spec }
func (r *recordingTool) Run(_ context.Context, _ json.RawMessage) (proposal.EvidenceRef, error) {
	r.calls++
	return r.ref, nil
}

type staticTopo struct{ snap proposal.TopologySnapshot }

func (s staticTopo) Topology(_ context.Context, _ signal.Detection) (proposal.TopologySnapshot, error) {
	return s.snap, nil
}

type staticChange struct{ snap proposal.ChangeSnapshot }

func (s staticChange) Changes(_ context.Context, _ signal.Detection) (proposal.ChangeSnapshot, error) {
	return s.snap, nil
}

// scriptedModel replays a fixed Completion sequence, so the reason loop runs
// deterministically and with no API key. It stands in for the provider only —
// Tools and Intake are still faked separately, by recordingTool/staticTopo/staticChange.
type scriptedModel struct {
	script []clank.Completion
	i      int
}

func (m *scriptedModel) Complete(_ context.Context, _ []clank.Message, _ []clank.ToolSpec) (clank.Completion, error) {
	if m.i >= len(m.script) {
		return clank.Completion{}, nil
	}
	c := m.script[m.i]
	m.i++
	return c, nil
}

func proposeArgs(t *testing.T, ps proposal.Set) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}
	return b
}

// newEngine wires the full engine around model, faking Tools/Intake either way —
// model may be a scripted double or the real provider, shared by the hermetic
// and eval-gated live tests alike.
func newEngine(t *testing.T, model clank.Model, tool clank.Tool, catalog *contract.StaticCatalog) (*clank.Engine, *publishtest.CapturePublisher[proposal.Set]) {
	t.Helper()
	pub := &publishtest.CapturePublisher[proposal.Set]{}
	return &clank.Engine{
		Intake: clank.NewIntake(
			staticTopo{proposal.TopologySnapshot{Downstream: []proposal.NodeState{
				{Name: "payments-db", State: "degraded", TrafficShare: 0.7},
			}}},
			staticChange{proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model:        model,
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
		ref: proposal.EvidenceRef{
			Tool: "metrics", Query: "payments-db-cpu",
			Summary: "payments-db CPU pinned at 99%, connection pool exhausted",
			Ref:     "metrics://payments-db/cpu", Live: true,
		},
	}
	catalog := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassResourceExhaustion},
		ApplicableTiers:          []string{"tier-1"},
	}})
	model := &scriptedModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassResourceExhaustion,
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.9,
				Citations: []string{"payments-db-cpu"},
			}},
		})}}},
	}}

	e, sink := newEngine(t, model, metrics, catalog)
	set, err := e.Propose(t.Context(), goldenSignal())
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
	// The catalog carries exactly one contract, so this is a decode/plumbing
	// check on enforceCatalog, not a behavioural proof against a real model —
	// that proof lives in internal/clank's production-catalog eval.
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
	if len(sink.Delivered) != 1 {
		t.Errorf("a passed set is delivered exactly once; delivered %d", len(sink.Delivered))
	}
}

func TestEngine_ThinEvidence_YieldsNoActionAndDeliversNothing(t *testing.T) {
	metrics := &recordingTool{
		spec: clank.ToolSpec{Name: "metrics", Description: "read-only telemetry query for a service's live metrics"},
		ref:  proposal.EvidenceRef{Tool: "metrics", Query: "payments-db-cpu", Summary: "all services nominal; no anomaly on payments-db", Ref: "metrics://payments-db/cpu", Live: true},
	}
	catalog := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
	}})
	// The script runs out after the metrics call, so Complete's next turn
	// returns an empty Completion — the engine reads that as a decline. This
	// pins the engine's plumbing on a decline, not whether a real model would
	// judge "all nominal" evidence as actionable; that judgment stays with the
	// eval harness, where only a real model's output can prove it.
	model := &scriptedModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{}`)}}},
	}}

	e, sink := newEngine(t, model, metrics, catalog)
	set, err := e.Propose(t.Context(), goldenSignal())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if set.Status.Phase == "proposed" {
		t.Errorf("a declined turn should not reach a proposal: %+v", set)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a non-proposed set must deliver nothing; delivered %d", len(sink.Delivered))
	}
}
