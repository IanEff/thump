package clank_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish/publishtest"
	"github.com/ianeff/thump/internal/reason"
)

func TestPropose_WithEvidence_YieldsARankedProposalSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		// turn 1: gather live evidence
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		// turn 2: propose - hypothesis + a candidate drawn from the catalog
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	if !got.Gate.Passed || len(got.Proposals) == 0 {
		t.Fatalf("an evidence-backed signal should yield a passed, non-empty ProposalSet: %+v", got)
	}
	if diff := cmp.Diff(got.Proposals[0].ID, got.Recommended); diff != "" {
		t.Error("recommended must be the rank-1 proposal (-want +got)\n", diff)
	}
}

// TestPropose_GateDeclineSurfacesReason pins the other half of the "mute
// decline" bug: a model that DOES propose (unlike the insufficient-evidence
// path TestGoldenPath_ArgocdSyncDeclinesWithALegibleReason exercises) and
// cites evidence it actually gathered — so the citation check clears — but
// that evidence is never Live, so the readiness gate itself declines. That
// decline must say why (Status.Reason), not just what (Status.Phase).
func TestPropose_GateDeclineSurfacesReason(t *testing.T) {
	t.Parallel()
	tool := fakeTool{name: "logs", digest: "no live signal", ref: "loki:xyz", live: false, query: "log_scan", key: "log_scan"}
	model := &fakeModel{script: []reason.Completion{
		// turn 1: gather evidence that is never Live
		{ToolCalls: []reason.ToolCall{{Name: "logs", Args: json.RawMessage(`{"q":"log_scan"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{"log_scan"}}},
		})}}},
	}}

	e, sink := newTestEngine(model)
	e.Tools = map[string]reason.Tool{"logs": tool}
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	if got.Gate.Passed {
		t.Fatalf("no live evidence should fail the gate, got Passed=true: %+v", got.Gate)
	}
	if len(sink.Delivered) != 0 {
		t.Fatalf("a gate decline delivers nothing; delivered %d", len(sink.Delivered))
	}
	if got.Status.Phase != "no_action" {
		t.Errorf("a gate decline is phase=no_action, got %q", got.Status.Phase)
	}
	if got.Status.Reason == "" {
		t.Fatal("gate decline is mute: Status.Reason is empty despite GateResult.Reason being set")
	}
	if diff := cmp.Diff(got.Gate.Reason, got.Status.Reason); diff != "" {
		t.Error("Status.Reason must mirror GateResult.Reason (-want +got)\n", diff)
	}
}

// withJournal wires a capture double onto e.Journal, so a test can assert on
// what was journaled independently of what Pub delivered.
func withJournal(e *clank.Engine) *publishtest.CapturePublisher[proposal.Set] {
	journal := &publishtest.CapturePublisher[proposal.Set]{}
	e.Journal = journal
	return journal
}

func TestPropose_JournalsEveryTerminalPhase(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		model     func(t *testing.T) reason.Model
		configure func(e *clank.Engine)
		wantPhase string
	}{
		"Propose journals a run that exhausted its step budget without proposing": {
			model: func(*testing.T) reason.Model {
				metrics := reason.Completion{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}}
				return &fakeModel{script: []reason.Completion{metrics, metrics, metrics, metrics}}
			},
			configure: func(e *clank.Engine) { e.MaxSteps = 3 },
			wantPhase: "budget_exhausted",
		},
		"Propose journals a run whose model declined to name any action": {
			model: func(*testing.T) reason.Model {
				return &fakeModel{script: []reason.Completion{{ToolCalls: []reason.ToolCall{{
					Name: "insufficient",
					Args: json.RawMessage(`{"reason":"no live corroboration for the topology hypothesis"}`),
				}}}}}
			},
			wantPhase: "no_action",
		},
		"Propose journals a run whose recommended candidate failed the gate": {
			model: func(*testing.T) reason.Model {
				return &fakeModel{script: []reason.Completion{
					{ToolCalls: []reason.ToolCall{{Name: "logs", Args: json.RawMessage(`{"q":"log_scan"}`)}}},
					{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
						FailureClass: proposal.ClassDependencySaturation,
						Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
						Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{"log_scan"}}},
					})}}},
				}}
			},
			configure: func(e *clank.Engine) {
				e.Tools = map[string]reason.Tool{"logs": fakeTool{name: "logs", digest: "no live signal", ref: "loki:xyz", live: false, query: "log_scan", key: "log_scan"}}
			},
			wantPhase: "no_action",
		},
		"Propose journals a run that passed the gate, the same as one that did not": {
			model: func(*testing.T) reason.Model {
				return &fakeModel{script: []reason.Completion{
					{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
					{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
						FailureClass: proposal.ClassDependencySaturation,
						Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
						Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{`{"q":"latency_p99"}`}}},
					})}}},
				}}
			},
			wantPhase: "proposed",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e, _ := newTestEngine(tc.model(t))
			if tc.configure != nil {
				tc.configure(e)
			}
			journal := withJournal(e)

			got, err := e.Propose(context.Background(), sigBurnAccel())
			if err != nil {
				t.Fatalf("Propose errored: %v", err)
			}
			if diff := cmp.Diff(tc.wantPhase, got.Status.Phase); diff != "" {
				t.Error("wrong terminal phase (-want +got)\n", diff)
			}
			if len(journal.Delivered) != 1 {
				t.Fatalf("journal recorded %d sets, want exactly 1 for every terminal phase", len(journal.Delivered))
			}
			if diff := cmp.Diff(tc.wantPhase, journal.Delivered[0].Status.Phase); diff != "" {
				t.Error("journaled set has the wrong phase (-want +got)\n", diff)
			}
			if got.RunID == "" {
				t.Error("Propose returned a set with no RunID — every terminal phase must stamp one, not just proposed, so calipers transcript can join a sealed transcript back to it")
			}
			if diff := cmp.Diff(got.RunID, journal.Delivered[0].RunID); diff != "" {
				t.Error("journaled set's RunID must match the run it came from (-want +got)\n", diff)
			}
		})
	}
}

func TestPropose_PublishesOnlyGatePassingSetsToTheProposalsSubject(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		model         func(t *testing.T) reason.Model
		configure     func(e *clank.Engine)
		wantDelivered int
	}{
		"a budget-exhausted run journals but never reaches thump.proposals": {
			model: func(*testing.T) reason.Model {
				metrics := reason.Completion{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}}
				return &fakeModel{script: []reason.Completion{metrics, metrics, metrics, metrics}}
			},
			configure: func(e *clank.Engine) { e.MaxSteps = 3 },
		},
		"a gate-failed run journals but never reaches thump.proposals": {
			model: func(*testing.T) reason.Model {
				return &fakeModel{script: []reason.Completion{
					{ToolCalls: []reason.ToolCall{{Name: "logs", Args: json.RawMessage(`{"q":"log_scan"}`)}}},
					{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
						FailureClass: proposal.ClassDependencySaturation,
						Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
						Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{"log_scan"}}},
					})}}},
				}}
			},
			configure: func(e *clank.Engine) {
				e.Tools = map[string]reason.Tool{"logs": fakeTool{name: "logs", digest: "no live signal", ref: "loki:xyz", live: false, query: "log_scan", key: "log_scan"}}
			},
		},
		"a gate-passing run reaches thump.proposals, the same set the journal recorded": {
			model: func(*testing.T) reason.Model {
				return &fakeModel{script: []reason.Completion{
					{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
					{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
						FailureClass: proposal.ClassDependencySaturation,
						Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
						Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{`{"q":"latency_p99"}`}}},
					})}}},
				}}
			},
			wantDelivered: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e, pub := newTestEngine(tc.model(t))
			if tc.configure != nil {
				tc.configure(e)
			}
			journal := withJournal(e)

			if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
				t.Fatalf("Propose errored: %v", err)
			}
			if len(journal.Delivered) != 1 {
				t.Fatalf("journal recorded %d sets, want exactly 1", len(journal.Delivered))
			}
			if diff := cmp.Diff(tc.wantDelivered, len(pub.Delivered)); diff != "" {
				t.Error("wrong count reaching thump.proposals (-want +got)\n", diff)
			}
		})
	}
}

func TestPropose_StampsReversalAndBandFromTheCatalog(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			// bare — no ReversalPath, no GovernanceLevel, exactly what production omits
			Proposals: []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.87, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	cand := got.Proposals[0]
	if cand.ReversalPath == nil {
		t.Fatal("a catalogued, reversible action must have ReversalPath stamped, got nil")
	}
	if diff := cmp.Diff("unthrottle", cand.ReversalPath.Method); diff != "" {
		t.Error("ReversalPath.Method must come from the contract's Reversal.Method (-want +got)", diff)
	}
	if cand.GovernanceLevel == nil {
		t.Fatal("a reversible action must have GovernanceLevel stamped, got nil")
	}
	if diff := cmp.Diff(string(decision.BandActReversible), cand.GovernanceLevel.Band); diff != "" {
		t.Error("a reversible contract requests act_reversible (-want +got)", diff)
	}
}

// TestPropose_IrreversibleContractLeavesReversalNil is the honesty rider:
// stamping must never INVENT a reversal an action doesn't have — that would
// defeat hiss's I-12 irreversibility veto. An authored action with an empty
// Reversal must come out of Propose with ReversalPath still nil.
func TestPropose_IrreversibleContractLeavesReversalNil(t *testing.T) {
	t.Parallel()
	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "cordon-node",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		// Reversal deliberately zero-value — this action genuinely can't be undone
	}})

	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "cordon-node", Confidence: 0.9, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngineWithCatalog(model, cat)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	cand := got.Proposals[0]
	if cand.ReversalPath != nil {
		t.Errorf("an action with no authored Reversal must not get a fabricated ReversalPath, got %+v", cand.ReversalPath)
	}
	if cand.GovernanceLevel == nil || cand.GovernanceLevel.Band != string(decision.BandActDisruptive) {
		t.Errorf("an irreversible action's requested band must be act_disruptive, got %+v", cand.GovernanceLevel)
	}
}

// TestPropose_StampsPredictedImpactFromTheCatalog pins the producer half of
// the effectiveness delta: enrichFromCatalog copies the authored
// SeverityReductionPct onto the candidate the same way it copies BlastTier and
// the reversal, so recordEffectiveness has a forecast to score the observed
// reduction against. hold-rebalance authors a 0.7 baseline.
func TestPropose_StampsPredictedImpactFromTheCatalog(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"ceph_health"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassRedundancyDegraded,
			Hypotheses:   []proposal.Hypothesis{{Name: "redundancy_degraded", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "hold-rebalance", Confidence: 0.82, Citations: []string{`{"q":"ceph_health"}`}}},
		})}}},
	}}

	e, _ := newTestEngineWithCatalog(model, clank.ShippedCatalogForTest())
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	cand := got.Proposals[0]
	if cand.PredictedImpact == nil {
		t.Fatal("a catalogued action with an authored SeverityReductionPct must have PredictedImpact stamped, got nil")
	}
	if diff := cmp.Diff(0.7, cand.PredictedImpact.SeverityReductionPct); diff != "" {
		t.Error("PredictedImpact.SeverityReductionPct must come from the contract's authored baseline (-want +got)", diff)
	}
}

// TestPropose_UnforecastContractLeavesPredictedImpactNil is the effectiveness
// honesty rider, mirroring the reversal one: an action the catalog gives no
// SeverityReductionPct must come out of Propose with PredictedImpact nil — a
// zero baseline is unforecast, not a forecast of no effect, so
// recordEffectiveness skips it rather than scoring a fabricated win.
func TestPropose_UnforecastContractLeavesPredictedImpactNil(t *testing.T) {
	t.Parallel()
	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "cordon-node",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		Reversal:                 contract.Reversal{Method: "uncordon-node"},
		// SuccessCriteria.SeverityReductionPct deliberately zero — this action forecasts nothing
	}})

	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "cordon-node", Confidence: 0.9, Citations: []string{`{"q":"latency_p99"}`}}},
		})}}},
	}}

	e, _ := newTestEngineWithCatalog(model, cat)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}

	if cand := got.Proposals[0]; cand.PredictedImpact != nil {
		t.Errorf("an action with no authored SeverityReductionPct must not get a fabricated PredictedImpact, got %+v", cand.PredictedImpact)
	}
}

func TestScoreConfidence_TableOverGroundingClasses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		signalConf   float64
		corroborated int // citations resolving to live, in-topology refs
		selfReport   float64
		want         float64
	}{
		"scoreConfidence caps an uncorroborated candidate well below its self-report": {
			signalConf: 0.9, corroborated: 0, selfReport: 0.95, want: 0.27,
		},
		"scoreConfidence lets two corroborated citations carry the signal confidence through": {
			signalConf: 0.9, corroborated: 2, selfReport: 0.95, want: 0.9,
		},
		"scoreConfidence honors a self-report lower than the computed grounding": {
			signalConf: 0.9, corroborated: 2, selfReport: 0.6, want: 0.6,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := clank.ScoreConfidenceForTest(tc.signalConf, tc.corroborated, tc.selfReport)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong computed confidence (-want +got)\n", diff)
			}
		})
	}
}

func TestCoherentLiveCitations_CountsASelfSubjectCitationTowardGrounding(t *testing.T) {
	t.Parallel()

	// The same shape gate_test.go's "admits a live citation about the
	// affected service itself" pins: the sole citation is tagged to the
	// signal's own OriginService, absent from every topology list. The gate
	// and the confidence scorer must agree this citation grounds — this is
	// the twin that pins they moved together off the shared predicate.
	sao := &proposal.SAO{
		Signal: proposal.SignalSnapshot{OriginService: "product-catalog"},
		Topology: proposal.TopologySnapshot{
			Upstream: []proposal.NodeState{{Name: "frontend", State: "healthy"}},
		},
	}
	cand := proposal.Candidate{Citations: []string{"self_check"}}
	evidence := []proposal.EvidenceRef{{Key: "self_check", Live: true, Subject: "product-catalog"}}

	got := clank.CoherentLiveCitationsForTest(cand, evidence, sao)
	if diff := cmp.Diff(1, got); diff != "" {
		t.Error("wrong corroboration count for a self-subject live citation (-want +got)\n", diff)
	}
}

// TestCoherentLiveCitations_CountsDistinctBackendsNotRefs pins W5 (defence
// 1): the ≥2-source floor scoreConfidence applies to Corroborated must be
// satisfiable only by distinct backends, not by ref count. Before this
// lands, a candidate citing the same backend under two query names clears
// GroundingMany exactly as if it had corroboration from two independent
// tools — the gap thump-running-notes.md recorded on 2026-07-27.
func TestCoherentLiveCitations_CountsDistinctBackendsNotRefs(t *testing.T) {
	t.Parallel()

	sao := &proposal.SAO{Signal: proposal.SignalSnapshot{OriginService: "product-catalog"}}

	tests := map[string]struct {
		citations []string
		evidence  []proposal.EvidenceRef
		want      int
	}{
		"one backend queried under two different query names counts as one source": {
			citations: []string{"q1", "q2"},
			evidence: []proposal.EvidenceRef{
				{Tool: "loki", Key: "q1", Live: true, Subject: "product-catalog"},
				{Tool: "loki", Key: "q2", Live: true, Subject: "product-catalog"},
			},
			want: 1,
		},
		"two distinct backends each queried once count as two sources": {
			citations: []string{"q1", "q2"},
			evidence: []proposal.EvidenceRef{
				{Tool: "loki", Key: "q1", Live: true, Subject: "product-catalog"},
				{Tool: "kube", Key: "q2", Live: true, Subject: "product-catalog"},
			},
			want: 2,
		},
		"the same backend cited three times still counts as one source": {
			citations: []string{"q1", "q2", "q3"},
			evidence: []proposal.EvidenceRef{
				{Tool: "loki", Key: "q1", Live: true, Subject: "product-catalog"},
				{Tool: "loki", Key: "q2", Live: true, Subject: "product-catalog"},
				{Tool: "loki", Key: "q3", Live: true, Subject: "product-catalog"},
			},
			want: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cand := proposal.Candidate{Citations: tc.citations}

			got := clank.CoherentLiveCitationsForTest(cand, tc.evidence, sao)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong corroboration count across the evidence set (-want +got)\n", diff)
			}
		})
	}
}

// noChangeIntake builds an Intake with an empty ChangeSnapshot, so
// scoreConfidence's causal term drops out entirely (LikelihoodOK false) —
// isolating a test to the citation-grounding term alone, the way a real
// production run does today (noopChange{}, CLAUDE.md § rattle).
func noChangeIntake() *clank.Intake {
	return clank.NewIntake(
		fakeTopo{snap: proposal.TopologySnapshot{
			Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
		}},
		fakeChange{snap: proposal.ChangeSnapshot{}},
	)
}

func TestPropose_AnUncorroboratedCandidateCannotKeepItsSelfReportedConfidence(t *testing.T) {
	t.Parallel()

	// The model asserts 0.95 while citing a real evidence ref the run
	// gathered (so K1's "cite something you actually looked at" check
	// passes) — but that ref is not Live, e.g. a case-base/historical
	// lookup rather than fresh telemetry. Whatever the coefficients, a
	// self-report with no inspectable grounding must be pulled below
	// itself — otherwise the emitted number is the model's opinion wearing
	// the audit trail's clothes.
	const selfReported = 0.95
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "history", Args: json.RawMessage(`{"q":"past_incidents"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: selfReported,
				Citations: []string{"past_incidents"},
			}},
		})}}},
	}}

	eng, _ := newTestEngine(model)
	eng.Intake = noChangeIntake()
	eng.Tools["history"] = fakeTool{name: "history", digest: "3 similar incidents on file", live: false, query: "past_incidents", key: "past_incidents"}

	got, err := eng.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	conf := got.ConfidenceFor(got.Recommended)
	if conf >= selfReported {
		t.Errorf("uncorroborated candidate kept its self-report: got %v, want < %v", conf, selfReported)
	}
}

func TestPropose_ASelfReportLowersButNeverRaisesTheComputedConfidence(t *testing.T) {
	t.Parallel()

	// Two runs, identical grounding (one Live, in-topology citation, no
	// change events), only the self-report differs: 0.99 and 0.30. A
	// self-report above the computed grounding can't push the emitted
	// number past it; a self-report below it still pulls the number down.
	run := func(selfReported float64) float64 {
		model := &fakeModel{script: []reason.Completion{
			{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
			{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
				FailureClass: proposal.ClassDependencySaturation,
				Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
				Proposals: []proposal.Candidate{{
					ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: selfReported,
					Citations: []string{`{"q":"latency_p99"}`},
				}},
			})}}},
		}}
		eng, _ := newTestEngine(model)
		eng.Intake = noChangeIntake()

		got, err := eng.Propose(context.Background(), sigBurnAccel())
		if err != nil {
			t.Fatal(err)
		}
		return got.ConfidenceFor(got.Recommended)
	}

	// The grounding ceiling: one corroborated citation, no self-report
	// low enough to constrain it (1.0 clears anything scoreConfidence
	// could compute here) — the same pure function K3's table test locks.
	ceiling := clank.ScoreConfidenceForTest(0.9, 1, 1.0)

	if diff := cmp.Diff(ceiling, run(0.99)); diff != "" {
		t.Error("a self-report above the computed grounding must not raise it (-want +got)\n", diff)
	}
	if diff := cmp.Diff(0.30, run(0.30)); diff != "" {
		t.Error("a self-report below the computed grounding must still lower it (-want +got)\n", diff)
	}
}

// TestEngine_ToolSpecsAreSortedByName pins tool order to a sort, not map
// iteration — a cache_control breakpoint on the tool catalog caches nothing
// if the rendered order differs turn to turn.
func TestEngine_ToolSpecsAreSortedByName(t *testing.T) {
	t.Parallel()

	e := &clank.Engine{Tools: map[string]reason.Tool{
		"metrics": fakeTool{name: "metrics"},
		"kube":    fakeTool{name: "kube"},
		"loki":    fakeTool{name: "loki"},
	}}
	want := []string{"insufficient", "kube", "loki", "metrics", "propose"}

	for range 20 { // each call ranges e.Tools fresh, so 20 calls exercise 20 different random start points
		var got []string
		for _, s := range clank.ToolSpecsForTest(e) {
			got = append(got, s.Name)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatal("tool specs not sorted by name", diff)
		}
	}
}

type fakeModel struct {
	script        []reason.Completion
	err           error // when set, Complete fails on every call regardless of script — simulates a Model outage
	i             int
	calls         int
	received      [][]reason.Message
	receivedTools [][]reason.ToolSpec
}

func (m *fakeModel) Complete(_ context.Context, msgs []reason.Message, tools []reason.ToolSpec) (reason.Completion, error) {
	m.calls++
	cp := make([]reason.Message, len(msgs))
	copy(cp, msgs)
	m.received = append(m.received, cp)
	m.receivedTools = append(m.receivedTools, tools)
	if m.err != nil {
		return reason.Completion{}, m.err
	}
	if m.i >= len(m.script) {
		return reason.Completion{}, nil // ran out of script -> no tool calls -> loop ends
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

type metricsTool struct{}

func (metricsTool) Run(_ context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{
		Tool:    "metrics",
		Query:   string(args),
		Key:     string(args),
		Summary: "latency_p99 elevated 3x over baseline",
		Ref:     "metrics://latency_p99",
		Live:    true,
		// Subject names a node in newTestEngine/newTestEngineWithCatalog's
		// fixed fakeTopo snapshot (Downstream: payments-db) — signal-
		// independent, so this citation is in-topology regardless of which
		// fixture's OriginService the caller uses (W3: gate.go's
		// coherentSubject fails closed on an untagged ref).
		Subject: "payments-db",
	}, nil
}

func (metricsTool) Spec() reason.ToolSpec {
	return reason.ToolSpec{Name: "metrics", Description: "read-only telemetry query"}
}

// logsTool is the second backend the test engine needs to reach the
// two-source grounding tier at all: the tier counts distinct EvidenceRef.Tool
// values, so a run citing metricsTool twice is corroborated once. Same fixed
// Subject as metricsTool, for the same reason.
type logsTool struct{}

func (logsTool) Run(_ context.Context, args json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{
		Tool:    "loki",
		Query:   string(args),
		Summary: "3 log line(s); last: connection pool exhausted",
		Ref:     "loki://payments/payments-db",
		Live:    true,
		Subject: "payments-db",
	}, nil
}

func (logsTool) Spec() reason.ToolSpec {
	return reason.ToolSpec{Name: "loki", Description: "read-only log query"}
}

func newTestEngine(model reason.Model) (*clank.Engine, *publishtest.CapturePublisher[proposal.Set]) {
	pub := &publishtest.CapturePublisher[proposal.Set]{}
	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: proposal.TopologySnapshot{
				Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model: model,
		Tools: map[string]reason.Tool{"metrics": metricsTool{}, "loki": logsTool{}},
		Catalog: contract.NewStaticCatalog([]contract.ActionContract{{
			Name:                     "throttle-non-critical-paths",
			ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
			ApplicableTiers:          []string{"tier-1"},
			Reversal:                 contract.Reversal{Method: "unthrottle", Fallback: "page-oncall"},
		}}),
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		Ledger:       clank.NewMemProposalLog(),
		Pub:          pub,
		MaxSteps:     8,
		Weights:      testWeights(),
	}, pub
}

// testWeights layers scoreConfidence's grounding tiers onto uniformWeights
// (causal_test.go) — every engine test that runs the full Propose loop gets
// a real, nonzero CausalScore for its fixture's one fake change event,
// instead of an unconfigured 0 silently zeroing out every candidate's
// emitted confidence.
func testWeights() clank.ScoringWeights {
	w := uniformWeights()
	w.GroundingNone, w.GroundingOne, w.GroundingMany = 0.3, 0.7, 1.0
	return w
}

func newTestEngineWithCatalog(model reason.Model, cat *contract.StaticCatalog) (*clank.Engine, *publishtest.CapturePublisher[proposal.Set]) {
	pub := &publishtest.CapturePublisher[proposal.Set]{}
	return &clank.Engine{
		Intake: clank.NewIntake(
			fakeTopo{snap: proposal.TopologySnapshot{
				Downstream: []proposal.NodeState{{Name: "payments-db", State: "degraded", TrafficShare: 0.7}},
			}},
			fakeChange{snap: proposal.ChangeSnapshot{Events: []proposal.ChangeEvent{
				{ID: "c1", Type: "deploy", Target: "payments-db", Age: 5 * time.Minute},
			}}},
		),
		Model:        model,
		Tools:        map[string]reason.Tool{"metrics": metricsTool{}, "loki": logsTool{}},
		Catalog:      cat,
		Ranker:       clank.NewRanker(),
		Gate:         clank.ReadinessGate{},
		Store:        clank.NewMemStore(),
		Scorer:       clank.NewCausalScorer(),
		DedupeWindow: time.Hour,
		Ledger:       clank.NewMemProposalLog(),
		Pub:          pub,
		MaxSteps:     8,
		Weights:      testWeights(),
	}, pub
}

type failingStore struct {
	*clank.MemStore
	failOn int
	calls  int
}

func (f *failingStore) Checkpoint(ctx context.Context, t clank.Turn) error {
	if f.calls == f.failOn {
		f.calls++
		return errors.New("disk on fire")
	}
	f.calls++
	return f.MemStore.Checkpoint(ctx, t)
}

// checkpointSpyStore records every checkpointed Turn, unlike MemStore.Pending,
// which excludes a run the moment Propose's deferred Finish marks it done and
// so can never answer "what did we ever checkpoint".
type checkpointSpyStore struct {
	*clank.MemStore
	checkpoints []clank.Turn
}

func (s *checkpointSpyStore) Checkpoint(ctx context.Context, t clank.Turn) error {
	s.checkpoints = append(s.checkpoints, t)
	return s.MemStore.Checkpoint(ctx, t)
}

type fakeTool struct {
	name   string
	digest string
	ref    string
	live   bool
	query  string
	key    string // left "" to exercise the engine-assigned fallback; set to pin a self-named citation
}

func (f fakeTool) Run(_ context.Context, _ json.RawMessage) (proposal.EvidenceRef, error) {
	return proposal.EvidenceRef{Tool: f.name, Summary: f.digest, Ref: f.ref, Live: f.live, Query: f.query, Key: f.key}, nil
}

func (f fakeTool) Spec() reason.ToolSpec {
	return reason.ToolSpec{Name: f.name, Description: "read-only"}
}

func specsContain(specs []reason.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

func specNames(specs []reason.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

func openProposalFor(fp string) proposal.Set {
	return proposal.Set{
		SignalRef: fp,
		Status:    &proposal.Status{Phase: "proposed"},
	}
}

func TestPropose_WhenModelDeclines_YieldsNoAction(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{
			Name: "insufficient",
			Args: json.RawMessage(`{"reason":"no live corroboration for the topology hypothesis"}`),
		}}},
	}}
	e, sink := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if diff := cmp.Diff("no_action", got.Status.Phase); diff != "" {
		t.Error("a declined investigation should be no_action (-want +got)\n", diff)
	}
	if diff := cmp.Diff("no live corroboration for the topology hypothesis", got.Status.Reason); diff != "" {
		t.Error("a reasoned decline must carry its reason (-want +got)\n", diff)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("no_action must deliver nothing: delivered %d", len(sink.Delivered))
	}
}

func TestPropose_InsufficientRecordsTheDiagnosedClass(t *testing.T) {
	t.Parallel()

	// A correct diagnosis with no catalogued remedy must survive as audit
	// data, not vanish into a bare decline — which classes accumulate
	// insufficient calls is the evidence any catalog addition waits on.
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{
			Name: "insufficient",
			Args: json.RawMessage(`{"reason":"no catalogued action lists this class","failureClass":"dependency_saturation"}`),
		}}},
	}}
	eng, _ := newTestEngine(model)

	got, err := eng.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(proposal.ClassDependencySaturation, got.FailureClass); diff != "" {
		t.Error("insufficient decline dropped the diagnosed class (-want +got)\n", diff)
	}
}

func TestPropose_StopsAtMaxSteps_YieldsBudgetExhausted(t *testing.T) {
	t.Parallel()
	metrics := reason.Completion{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}}
	model := &fakeModel{script: []reason.Completion{metrics, metrics, metrics, metrics}}
	e, sink := newTestEngine(model)

	e.MaxSteps = 3

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if got.Gate.BudgetOK {
		t.Errorf("exhausting MaxSteps must fail the budget minimum: %+v", got.Gate)
	}
	if diff := cmp.Diff("budget_exhausted", got.Status.Phase); diff != "" {
		t.Error("falling out of the loop should be budget_exhausted (-want +got)\n", diff)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("budget_exhausted delivers nothing %d", len(sink.Delivered))
	}
}

func TestPropose_HaltsWhenCheckpointFails(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"y"}`)}}}, // never reached
	}}
	e, _ := newTestEngine(model)
	e.Store = &failingStore{MemStore: clank.NewMemStore(), failOn: 0}
	_, err := e.Propose(context.Background(), sigBurnAccel())
	if err == nil {
		t.Fatal("a checkpoint failure must halt Propose with an error")
	}
	if model.calls != 1 {
		t.Errorf("run must halt at the failed checkpoint, not proceed: model.calls=%d", model.calls)
	}
}

func TestPropose_AppendsTheToolDigestToTheConversation(t *testing.T) {
	t.Parallel()
	const digest = "503 rate 12%/min on /checkout"
	tool := fakeTool{name: "logs", digest: digest, ref: "loki:abc", live: true}
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "logs", Args: json.RawMessage(`{"q":"errors"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"stub"}`)}}},
	}}
	e, _ := newTestEngine(model)
	e.Tools = map[string]reason.Tool{"logs": tool}

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	// Inv. 1 (digests only): a read-only tool's one-line EvidenceRef.Summary is
	// what enters the conversation — and that's *all* that can, since EvidenceRef
	// has no Raw field. This asserts the positive: the digest reached the model as
	// a tool-role message. (The old form scanned for a sentinel no tool ever
	// emitted, so it could never fail — a vacuous test with no teeth.)
	if !receivedToolDigest(model.received, digest) {
		t.Errorf("tool digest %q never reached the conversation:\n%+v", digest, model.received)
	}
}

func TestPropose_WhenModelEndsTurnWithoutATool_YieldsSyntheticReason(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{Message: reason.Message{Role: "assistant", Content: "I'm not sure what to do here."}},
	}}
	e, sink := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if diff := cmp.Diff("no_action", got.Status.Phase); diff != "" {
		t.Error("an empty-handed turn should still be no_action (-want +got)\n", diff)
	}
	if diff := cmp.Diff("model ended turn without a tool call", got.Status.Reason); diff != "" {
		t.Error("an empty-handed turn needs its own synthetic reason (-want +got)\n", diff)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("no_action must deliver nothing: delivered %d", len(sink.Delivered))
	}
}

func TestPropose_ToolMessagesCarryTheCitableKeyVerbatim(t *testing.T) {
	t.Parallel()

	// enforceCitations grades a candidate's citations against EvidenceRef.Key
	// by exact string equality, so every gathered ref's Key must appear
	// verbatim in a tool message the model received — a key the engine
	// validates but never showed is a check the model can only pass by luck.
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
	}}
	eng, _ := newTestEngine(model)

	got, err := eng.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Evidence) == 0 {
		t.Fatal("run gathered no evidence — the claim needs at least one ref to check")
	}

	final := model.received[len(model.received)-1]
	for _, ref := range got.Evidence {
		if ref.Key == "" {
			continue
		}
		if !receivedToolContent(final, ref.Key) {
			t.Errorf("citable key %q never reached the conversation:\n%+v", ref.Key, final)
		}
	}
}

func TestEngine_Propose_ChecksPointsTheRealChangeTarget(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"no evidence yet"}`)}}},
	}}
	store := &checkpointSpyStore{MemStore: clank.NewMemStore()}
	e, _ := newTestEngine(model)
	e.Store = store

	if _, err := e.Propose(t.Context(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, turn := range store.checkpoints {
		for _, msg := range turn.Msgs {
			if strings.Contains(msg.Content, "payments-db") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the checkpointed transcript lost the real change target")
	}
}

// receivedToolContent reports whether any tool-role message in one conversation
// snapshot contains the key verbatim — substring, not equality, because the key
// rides inside the digest line rather than replacing it.
func receivedToolContent(msgs []reason.Message, key string) bool {
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		for _, r := range m.ToolResults {
			if strings.Contains(r.Digest, key) {
				return true
			}
		}
	}
	return false
}

// receivedToolDigest reports whether any message snapshot shown to the model
// carries the digest as a tool-role message — i.e. the engine forwarded the
// one-line EvidenceRef.Summary into the conversation.
func receivedToolDigest(snapshots [][]reason.Message, digest string) bool {
	for _, msgs := range snapshots {
		for _, m := range msgs {
			if m.Role != "tool" {
				continue
			}
			for _, r := range m.ToolResults {
				if strings.Contains(r.Digest, digest) {
					return true
				}
			}
		}
	}
	return false
}

func TestPropose_OffersReadOnlyToolsAndControlVerbs(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{{ToolCalls: []reason.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"stub"}`)}}}}}
	e, _ := newTestEngine(model)
	e.Tools = map[string]reason.Tool{
		"metrics":  metricsTool{},
		"casebase": fakeTool{name: "casebase", digest: "similar incident 3w ago", ref: "cb:1"},
	}

	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	if len(model.receivedTools) == 0 {
		t.Fatal("model wasn't offered any tool specs")
	}
	offered := model.receivedTools[0]

	// A real model can only emit a tool call for a tool it was offered, so the
	// read-only telemetry tools AND the two terminal control verbs must all be
	// on the table — otherwise the loop can never terminate via propose/insufficient.
	for _, name := range []string{"metrics", "casebase", "propose", "insufficient"} {
		if !specsContain(offered, name) {
			t.Errorf("expected %q to be offered to the model: %v", name, specNames(offered))
		}
	}

	// The autonomy boundary: a catalogued action is never a callable tool. The
	// model names it by ref inside propose's args, where enforceCatalog gates it.
	if specsContain(offered, "throttle-non-critical-paths") {
		t.Errorf("a catalogued action must not be offered as a callable tool: %v", specNames(offered))
	}
}

func TestPropose_RejectsACandidateOutsideTheCatalog(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "neerdowell", ContractRef: "rm -rf"}},
		})}}},
	}}

	e, sink := newTestEngine(model)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("an off-catalog ref must decline the run, not error it: got %v", err)
	}
	if diff := cmp.Diff("no_action", got.Status.Phase); diff != "" {
		t.Error("an off-catalog ref must end as a recorded decline (-want +got)\n", diff)
	}
	if got.Status.Reason == "" {
		t.Fatal("an off-catalog decline is mute: Status.Reason is empty")
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a rejected set must never be delivered: %d", len(sink.Delivered))
	}
}

// TestPropose_ClassMismatchBecomesAnAuditableDecline pins the fix for the
// 2026-07-13 discrimination bug (thump-running-notes.md): unlike a wholly
// invented ContractRef (the test above), "hold-rebalance" here IS a real
// catalogued action — it's just not applicable to the class the model
// declared. That must become a legible no_action decline recorded to the
// ledger, never a returned error (which would leave the whole run
// unaudited) and never delivered.
func TestPropose_ClassMismatchBecomesAnAuditableDecline(t *testing.T) {
	t.Parallel()
	cat := contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "hold-rebalance",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassResourceExhaustion},
		ApplicableTiers:          []string{"tier-1"},
	}})
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassUnknown,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "hold-rebalance"}},
		})}}},
	}}

	e, sink := newTestEngineWithCatalog(model, cat)
	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatalf("a class mismatch must not error the whole run, got %v", err)
	}
	if got.Status.Phase != "no_action" {
		t.Errorf("phase = %q, want no_action", got.Status.Phase)
	}
	if got.Status.Reason == "" {
		t.Fatal("a class-mismatch decline is mute: Status.Reason is empty")
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a declined set must never be delivered: %d", len(sink.Delivered))
	}
}

func TestPropose_RecordsTheCitationsEachCandidateCarries(t *testing.T) {
	t.Parallel()

	// The citation list must survive the round trip untouched: what the model
	// cited is what the audit trail carries — the gate and the confidence
	// function read this list, so a dropped or reordered citation would change
	// what the machine believes it verified.
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.9,
				Citations: []string{`{"q":"latency_p99"}`},
			}},
		})}}},
	}}

	eng, _ := newTestEngine(model)
	got, err := eng.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{`{"q":"latency_p99"}`}
	if diff := cmp.Diff(want, got.Proposals[0].Citations); diff != "" {
		t.Error("candidate citations did not survive the round trip (-want +got)\n", diff)
	}
}

func TestPropose_DeclinesACandidateCitingEvidenceTheRunNeverGathered(t *testing.T) {
	t.Parallel()

	// A citation naming a query the loop never issued is a causal claim with
	// no inspectable basis — the run must end as an auditable no_action, never
	// a delivered set. This is the same refusal shape as a class-mismatched
	// contract ref: recorded and loud, not silent.
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.9,
				Citations: []string{`{"q":"a_query_never_issued"}`},
			}},
		})}}},
	}}

	eng, sink := newTestEngine(model)
	got, err := eng.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != proposal.PhaseNoAction {
		t.Errorf("an ungrounded citation must decline, got phase %q (reason %q)", got.Status.Phase, got.Status.Reason)
	}
	if len(sink.Delivered) != 0 {
		t.Error("an ungrounded citation must never be delivered", cmp.Diff(0, len(sink.Delivered)))
	}
}

// TestPropose_KubeCitesByItsAssignedKeyNotTheRawCallItCannotRetype pins the
// 2026-08-13 live blocker: kube (and loki) stamp EvidenceRef.Query with the
// entire raw JSON of the model's own tool call — a multi-field blob a model
// reformats rather than recalls precisely several turns later, unlike
// metrics' short, self-chosen input.Q. A real run proposed disable-cart-
// failure with genuine two-tool corroboration and was rejected anyway: the
// kube citation didn't byte-match the query the run had, in fact, gathered.
func TestPropose_KubeCitesByItsAssignedKeyNotTheRawCallItCannotRetype(t *testing.T) {
	t.Parallel()

	const rawArgs = `{"resource":"pods","namespace":"rook-ceph","selector":{"app":"rook-ceph-mon","tier":"storage"}}`

	tests := map[string]struct {
		citation string
	}{
		"citing the raw call verbatim still declines — Query was never the key": {
			citation: rawArgs,
		},
		"citing the engine-assigned key clears the citation check": {
			citation: clank.EvidenceKeyForTest("kube", 0),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			model := &fakeModel{script: []reason.Completion{
				{ToolCalls: []reason.ToolCall{{Name: "kube", Args: json.RawMessage(rawArgs)}}},
				{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
					FailureClass: proposal.ClassDependencySaturation,
					Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
					Proposals: []proposal.Candidate{{
						ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.9,
						Citations: []string{tc.citation},
					}},
				})}}},
			}}

			e, _ := newTestEngine(model)
			e.Tools["kube"] = fakeTool{name: "kube", digest: "rook-ceph-mon-a (Running)", ref: "kube://rook-ceph/pods", live: true, query: rawArgs}

			got, err := e.Propose(context.Background(), sigBurnAccel())
			if err != nil {
				t.Fatal(err)
			}

			ungrounded := got.Status.Phase == proposal.PhaseNoAction && strings.Contains(got.Status.Reason, "did not gather")
			wantUngrounded := tc.citation == rawArgs
			if ungrounded != wantUngrounded {
				t.Errorf("citing %q: ungrounded=%v, want %v (phase %q, reason %q)",
					tc.citation, ungrounded, wantUngrounded, got.Status.Phase, got.Status.Reason)
			}
		})
	}
}

// TestPropose_MetricsKeepsItsOwnKeyWhileKubeGetsAnEngineAssignedOne pins the
// three-way name agreement engine.go's dispatch loop, clank.go's buildTools,
// and each evidence tool's Spec().Name used to leave untested: a tool that
// can name its own evidence in a form the model can retype (metrics) keeps
// that name as Key, and a tool that can't (kube) falls through to an
// engine-assigned one — decided per ref, not by a tool-name list.
func TestPropose_MetricsKeepsItsOwnKeyWhileKubeGetsAnEngineAssignedOne(t *testing.T) {
	t.Parallel()

	const rawMetricsArgs = `{"q":"latency_p99"}`
	const rawKubeArgs = `{"resource":"pods","namespace":"rook-ceph"}`
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{
			{Name: "metrics", Args: json.RawMessage(rawMetricsArgs)},
			{Name: "kube", Args: json.RawMessage(rawKubeArgs)},
		}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Hypotheses:   []proposal.Hypothesis{{Name: "dependency_saturation", Weight: 0.8}},
			Proposals: []proposal.Candidate{{
				ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.9,
				Citations: []string{rawMetricsArgs, clank.EvidenceKeyForTest("kube", 1)},
			}},
		})}}},
	}}

	e, _ := newTestEngine(model)
	e.Tools["kube"] = fakeTool{name: "kube", digest: "rook-ceph-mon-a (Running)", ref: "kube://rook-ceph/pods", live: true, query: rawKubeArgs}

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	if got.Gate == nil || !got.Gate.Passed {
		t.Fatalf("both citations should ground, got Gate=%+v Status=%+v", got.Gate, got.Status)
	}
	if diff := cmp.Diff(rawMetricsArgs, got.Evidence[0].Key); diff != "" {
		t.Error("metrics names its own citation key from input.Q (-want +got)", diff)
	}
	if diff := cmp.Diff(clank.EvidenceKeyForTest("kube", 1), got.Evidence[1].Key); diff != "" {
		t.Error("kube can't retype its raw call, so the engine assigns its key (-want +got)", diff)
	}
}

func TestPropose_SuppressesAnOpenDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sig := sigBurnAccel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.89, Citations: []string{`{"q":"x"}`}}},
		})}}},
	}}
	e, sink := newTestEngine(model)

	if err := e.Ledger.Record(ctx, openProposalFor(sig.Fingerprint)); err != nil {
		t.Fatal(err)
	}
	got, err := e.Propose(ctx, sig)
	if err != nil {
		t.Fatalf("Propose errored: %v", err)
	}
	if got.Gate != nil {
		t.Errorf("an open proposal on the same fingerprint must stop the run before a set forms: %+v", got.Gate)
	}
	if len(sink.Delivered) != 0 {
		t.Errorf("a suppressed fingerprint delivers nothing: %d", len(sink.Delivered))
	}
}

func TestPropose_FreezesTheSAOIntoTheSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Citations: []string{`{"q":"x"}`}}},
		})}}},
	}}
	e, _ := newTestEngine(model)

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}
	if got.SAOSnapshot.Version == 0 {
		t.Errorf("the SAO must be frozen onto the set for audit replay: %+v", got.SAOSnapshot)
	}
}

func TestPropose_AttachesCausalScoresToTheSet(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
		{ToolCalls: []reason.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
			FailureClass: proposal.ClassDependencySaturation,
			Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Citations: []string{`{"q":"x"}`}}},
		})}}},
	}}
	e, _ := newTestEngine(model)

	got, err := e.Propose(context.Background(), sigBurnAccel())
	if err != nil {
		t.Fatal(err)
	}

	if len(got.CausalScores) == 0 {
		t.Fatalf("the engine must score the change events onto the audit unit: %+v", got)
	}
	for _, cs := range got.CausalScores {
		if len(cs.Rationale) == 0 {
			t.Errorf("every causal score must carry its rationale, not just a number: %v", cs)
		}
	}
}

func TestSeedPrompt_StatesTheEvidenceStandardWithoutNamingAnyApp(t *testing.T) {
	t.Parallel()

	// The seed message is captured on the initial completion request even if no
	// tool calls are returned.
	model := &fakeModel{}
	eng, _ := newTestEngine(model)
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}
	seed := model.received[0][0].Content

	// The standard must be stated in rig-invariant terms (live, topology, cite).
	for _, want := range []string{"live", "topology", "cite"} {
		if !strings.Contains(seed, want) {
			t.Errorf("seed prompt is missing the evidence standard; expected %q in:\n%s", want, seed)
		}
	}
	// Verify no app-specific codenames or demo services are mentioned.
	for _, banned := range []string{"flagd", "cart", "ceph", "argocd"} {
		if strings.Contains(seed, banned) {
			t.Errorf("seed prompt names an app (%q) — rig knowledge belongs in config, not code:\n%s", banned, seed)
		}
	}
}

func TestSeedPrompt_TellsTheModelGroundingCountsDistinctTools(t *testing.T) {
	t.Parallel()

	model := &fakeModel{}
	eng, _ := newTestEngine(model)
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}
	seed := model.received[0][0].Content

	tests := map[string]struct {
		fragment string
		want     bool
	}{
		"the seed prompt states that grounding counts distinct tools": {
			fragment: "DISTINCT tools", want: true,
		},
		"the seed prompt asks for a second tool before proposing": {
			fragment: "second tool", want: true,
		},
		"the seed prompt no longer calls a single metric sufficient": {
			fragment: "sufficient in corroboration on its own", want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, strings.Contains(seed, tc.fragment)); diff != "" {
				t.Error("seed prompt disagrees with groundedConfidence about what grounds a proposal", diff)
			}
		})
	}
}

func TestSeedPrompt_StatesTheNoRemedyRule(t *testing.T) {
	t.Parallel()

	// The mislabel guard alone covers only one direction (don't force a class
	// because an action exists for it); the mirror case — correct class, no
	// catalogued action — needs its own stated terminal, or the model reaches
	// for the nearest action instead of declining.
	model := &fakeModel{}
	eng, _ := newTestEngine(model)
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}
	seed := model.received[0][0].Content

	if !strings.Contains(seed, "if no catalogued action lists your diagnosed failure class, call insufficient") {
		t.Errorf("seed prompt is missing the no-remedy rule:\n%s", seed)
	}
}

func TestSeedPrompt_RendersChangeEventsWhenTheSAOHasThem(t *testing.T) {
	t.Parallel()

	model := &fakeModel{}
	eng, _ := newTestEngine(model)
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}
	seed := model.received[0][0].Content

	if !strings.Contains(seed, "payments-db") {
		t.Errorf("seed prompt omits the SAO's change events; expected the deploy target in:\n%s",
			seed)
	}
}

// TestSeedPrompt_TellsTheModelWhatItsConfidenceCostsInBothDirections pins that
// the elicitation is two-sided: the shipped line says the model's number "can
// only lower it" (engine.go:577), which prices caution at zero and never says
// that a number under the governance floor sends the incident to a human.
func TestSeedPrompt_TellsTheModelWhatItsConfidenceCostsInBothDirections(t *testing.T) {
	t.Parallel()

	model := &fakeModel{}
	eng, _ := newTestEngine(model)
	if _, err := eng.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}
	seed := model.received[0][0].Content

	tests := map[string]struct {
		fragment string
		want     bool
	}{
		"the seed prompt explains that confidence is two-sided": {
			fragment: "two-sided", want: true,
		},
		"the seed prompt states the cost of under-confidence below the floor": {
			fragment: "floor", want: true,
		},
		"the seed prompt states that hedging sends the incident to a human": {
			fragment: "human", want: true,
		},
		"the seed prompt no longer states the one-sided can only lower it claim": {
			fragment: "can only lower it", want: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, strings.Contains(seed, tc.fragment)); diff != "" {
				t.Error("seed prompt disagrees with two-sided confidence elicitation", diff)
			}
		})
	}
}

func TestPropose_CarriesTheFiredSLOIdentityFromDetection(t *testing.T) {
	t.Parallel()

	model := &fakeModel{script: []reason.Completion{
		{ToolCalls: []reason.ToolCall{{
			Name: "insufficient",
			Args: json.RawMessage(`{"reason":"no live corroboration"}`),
		}}},
	}}
	eng, _ := newTestEngine(model)
	sig := sigBurnAccel()
	sig.SLORef = "cart-availability"

	got, err := eng.Propose(context.Background(), sig)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff("cart-availability", got.SLORef); diff != "" {
		t.Errorf("Engine.Propose SLORef mismatch (-want +got):\n%s", diff)
	}
}
