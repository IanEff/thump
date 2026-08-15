package probe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/probe"
	"github.com/ianeff/thump/internal/reason"
)

// TestRun_ReturnsOneRowPerRun pins the basic shape: n Propose calls in, n
// Rows out, each one grounded (Phase proposed) because the fake engine's
// evidence and topology line up — the row-count contract a caller (probe.Main
// or a future dashboard) can rely on regardless of what any individual run
// scored.
func TestRun_ReturnsOneRowPerRun(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine(t)
	rows, err := probe.Run(context.Background(), eng, sigPaymentsDB(), 3)
	if err != nil {
		t.Fatalf("Run errored: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows for 3 runs, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Run != i+1 {
			t.Errorf("row %d has Run=%d, want %d", i, r.Run, i+1)
		}
	}
}

// TestRun_ResetsTheLedgerBetweenCallsSoRepeatedRunsDoNotDedupCollide is the
// correctness property clank.ProbeReset exists for: without it, run 2 would
// see run 1's own set as an open dupe (same signal.Detection.Fingerprint,
// same DedupeWindow) and Propose would short-circuit it to a zero Set before
// ever reaching the model — a phase of "", not "no_action" or "proposed".
// That would read as every run past the first having failed, when nothing
// about the reasoning failed at all.
func TestRun_ResetsTheLedgerBetweenCallsSoRepeatedRunsDoNotDedupCollide(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine(t)
	rows, err := probe.Run(context.Background(), eng, sigPaymentsDB(), 5)
	if err != nil {
		t.Fatalf("Run errored: %v", err)
	}
	for _, r := range rows {
		if r.Phase != proposal.PhaseProposed {
			t.Errorf("run %d has Phase %q, want %q — a dedup collision would show up as a blank phase instead",
				r.Run, r.Phase, proposal.PhaseProposed)
		}
	}
}

// TestMain_CleanSkipsWithoutAnthropicAPIKey mirrors rca.Main's own
// convention: a probe run on an operator machine with no key configured
// exits 0, not 1 — never a hard failure for a missing key.
func TestMain_CleanSkipsWithoutAnthropicAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	var out, errOut bytes.Buffer
	got := probe.Main([]string{"-detection", "does-not-matter.yaml"}, &out, &errOut)
	if got != 0 {
		t.Errorf("Main returned %d with no ANTHROPIC_API_KEY set, want a clean skip (0)", got)
	}
}

// TestMain_RequiresADetectionFlag proves the usage error is probe's own —
// distinct from calipers's top-level usage line, so
// TestMain_RoutesEveryDocumentedVerbAndRefusesTheRest in the calipers
// package can tell "probe routed and refused its own args" apart from
// "probe never routed at all".
func TestMain_RequiresADetectionFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	got := probe.Main(nil, &out, &errOut)
	if got != 2 {
		t.Errorf("Main with no -detection returned %d, want 2", got)
	}
	if errOut.String() == "" {
		t.Error("Main with no -detection wrote nothing to stderr")
	}
}

// newFakeEngine builds a real *clank.Engine over fake evidence tools and a
// scripted Model — no live cluster, no ANTHROPIC_API_KEY. The fixture is
// deliberately grounded (payments-db is both the fake topology's one node
// and the fake tools' cited Subject) so every Propose call reaches
// PhaseProposed, which is what the dedup-collision test above depends on to
// tell a real result apart from a suppressed redelivery.
func newFakeEngine(t *testing.T) *clank.Engine {
	t.Helper()

	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		Reversal:                 contract.Reversal{Method: "unthrottle", Fallback: "page-oncall"},
	}})

	set := proposal.Set{
		FailureClass: proposal.ClassDependencySaturation,
		Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
		Proposals: []proposal.Candidate{{
			ID:          "p1",
			ContractRef: "throttle-non-critical-paths",
			Confidence:  0.9,
			Citations:   []string{`{"q":"latency_p99"}`},
		}},
	}
	args, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal propose args: %v", err)
	}

	model := &cyclicModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: args}}},
	}}

	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopology{snap: proposal.TopologySnapshot{
				Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChanges{},
		),
		Model:        model,
		Tools:        map[string]reason.Tool{"metrics": fakeMetricsTool{}},
		Catalog:      cat,
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		MaxSteps:     clank.DefaultLimits().MaxSteps,
		Weights:      clank.DefaultScoringWeights(),
	}
}

// cyclicModel replays a fixed script on a loop — Run calls Propose n times
// against the same *clank.Engine, and the script must still answer turn one
// the same way on run 5 as it did on run 1.
type cyclicModel struct {
	script []reason.Completion
	i      int
}

func (m *cyclicModel) Complete(context.Context, []reason.Message, []reason.ToolSpec) (reason.Completion, error) {
	c := m.script[m.i%len(m.script)]
	m.i++
	return c, nil
}

type fakeMetricsTool struct{}

func (fakeMetricsTool) Run(_ context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{
		Tool:    "metrics",
		Query:   string(args),
		Key:     string(args),
		Summary: "latency_p99 elevated 3x over baseline",
		Ref:     "metrics://latency_p99",
		Live:    true,
		Subject: "payments-db",
	}, nil
}

func (fakeMetricsTool) Spec() reason.ToolSpec {
	return reason.ToolSpec{Name: "metrics", Description: "read-only telemetry query"}
}

type fakeTopology struct{ snap proposal.TopologySnapshot }

func (f fakeTopology) Topology(context.Context, signal.Detection) (proposal.TopologySnapshot, error) {
	return f.snap, nil
}

type fakeChanges struct{}

func (fakeChanges) Changes(context.Context, signal.Detection) (proposal.ChangeSnapshot, error) {
	return proposal.ChangeSnapshot{}, nil
}

func sigPaymentsDB() signal.Detection {
	return signal.Detection{
		Name:          "payments-latency-burn-001",
		Fingerprint:   "fp-payments-latency-001",
		OriginService: "payments",
		ServiceTier:   "tier-1",
		DetectorType:  "burn_rate_acceleration",
		Divergence:    signal.Divergence{Metric: "latency_p99", Observed: 850, Baseline: 200, Confidence: 0.9, Trajectory: "accelerating"},
		Impact: signal.Impact{
			Severity:    signal.Severity{DegradationPct: 0.4, Trajectory: "accelerating"},
			BlastRadius: signal.BlastRadius{AffectedPct: 0.6, Velocity: "fast", DownstreamConsumers: 3},
		},
		DetectedAt: time.Now(),
	}
}
