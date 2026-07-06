package clank_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/broker"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/natstest"
	"github.com/ianeff/thump/internal/publish"
)

// TestClankLearnEdge_ClosesOverBroker is clank's slice of the five-beat seam
// (phase2-ws3-stage3b, step 4): a Detection published to thump.detections
// opens a ProposalSet in the Engine's ledger, and the matching Outcome
// published to thump.outcomes must be ABSORBED by the Learn edge — not
// orphaned — because both subscribers close over the SAME *MemProposalLog
// and *CaseBase (the A3 trap: a fresh ledger for Click finds every outcome's
// set missing and learns nothing, silently).
func TestClankLearnEdge_ClosesOverBroker(t *testing.T) {
	t.Parallel()
	js := natstest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := broker.EnsureTopology(ctx, js); err != nil {
		t.Fatal(err)
	}

	// runBroker-shaped wiring, but with a scripted fakeModel so it's keyless.
	ledger := clank.NewMemProposalLog()
	cases := clank.NewCaseBase()
	model := &fakeModel{script: []clank.Completion{
		// turn 1: gather live evidence
		{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		// turn 2: propose - hypothesis + a candidate drawn from the catalog
		{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, clank.ProposalSet{
			FailureClass: clank.ClassDependencySaturation,
			Hypotheses:   []clank.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []clank.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87}},
		})}}},
	}}
	eng := &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: clank.TopologySnapshot{
				Downstream: []clank.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: clank.ChangeSnapshot{Events: []clank.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model: model,
		Tools: map[string]clank.Tool{"metrics": metricsTool{}},
		Catalog: clank.NewStaticCatalog([]clank.ActionContract{{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []clank.FailureClass{clank.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
		}}),
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       &clank.CausalScorerImpl{Prior: cases},
		DedupeWindow: time.Hour,
		Ledger:       ledger,
		MaxSteps:     8,
	}
	// Click{Ledger, Cases} — THE SAME two instances the Engine reads/writes.
	learn := clank.Click{Ledger: ledger, Cases: cases}

	subCtx, stopSubs := context.WithCancel(ctx)
	defer stopSubs()

	detSub := broker.NewJetSubscriber[signal.Detection](js)
	go func() {
		_ = detSub.Run(subCtx, "thump.detections", func(ctx context.Context, det signal.Detection) error {
			_, err := eng.Propose(ctx, det)
			return err
		})
	}()

	outSub := broker.NewJetSubscriber[outcome.Outcome](js)
	go func() {
		_ = outSub.Run(subCtx, "thump.outcomes", func(ctx context.Context, o outcome.Outcome) error {
			_ = learn.Absorb(ctx, o) // mirror learnHandler: every disposition Acks, never errors
			return nil
		})
	}()

	// forward edge: a detection opens a proposal set in `ledger`
	pubDet := publish.NewJetPublisher[signal.Detection](js)
	if err := pubDet.Publish(ctx, "thump.detections", signal.Detection{Fingerprint: "fp-1", ServiceTier: "tier-1"}); err != nil {
		t.Fatal("publish detection:", err)
	}

	// ... run the detections subscriber until the set is open ...
	waitFor(t, "proposal set for fp-1 never opened", func() bool {
		open, err := ledger.Open(context.Background(), "fp-1", time.Time{})
		return err == nil && len(open) > 0
	})

	// return edge: the matching outcome must be ABSORBED, not orphaned
	pubOut := publish.NewJetPublisher[outcome.Outcome](js)
	if err := pubOut.Publish(ctx, "thump.outcomes", matchingOutcome("fp-1")); err != nil {
		t.Fatal("publish outcome:", err)
	}

	// ... run the outcomes subscriber ...
	waitFor(t, "outcome never absorbed into the case base", func() bool { return cases.Len() == 1 })

	if got := cases.Len(); got != 1 { // shared ledger ⇒ matched ⇒ one case learned
		t.Fatalf("case base has %d cases, want 1 — the Learn edge orphaned the outcome (A3 trap)", got)
	}
}

// waitFor polls cond every 20ms until it's true or 5s elapse.
func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func matchingOutcome(fingerprint string) outcome.Outcome {
	return outcome.Outcome{
		ID:          "out:" + fingerprint + ":1000",
		DecisionRef: "dec:" + fingerprint + ":1000",
		SignalRef:   fingerprint,
		ContractRef: "throttle-non-critical-paths",
		Mode:        outcome.ModeLive,
		Result:      outcome.ResultSuccess,
		ExecutedAt:  time.Unix(1000, 0),
	}
}
