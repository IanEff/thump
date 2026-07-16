package hiss_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/hiss"
	"sigs.k8s.io/yaml"
)

func TestEvaluate_GoldenPath_ApprovesAndStampsEverything(t *testing.T) {
	t.Parallel()
	got := decide(t, governedSet(), calmPolicy())

	if diff := cmp.Diff(goldenDecision(), got); diff != "" {
		t.Error("approved decision drifted from the golden fixture (-want +got)", diff)
	}
}

func TestEvaluate_EscalatesBelowTheConfidenceFloor(t *testing.T) {
	t.Parallel()
	ps := governedSet()
	ps.Proposals[0].Confidence = 0.60 // calmPolicy floor for tier-1 × dependency_saturation is 0.75

	got := decide(t, ps, calmPolicy())

	if diff := cmp.Diff(decision.VerdictEscalate, got.Verdict); diff != "" {
		t.Error("sub-floor confidence must escalate, not approve (-want +got)", diff)
	}
	if diff := cmp.Diff([]string{hiss.ReasonConfidenceFloor}, got.Reasons); diff != "" {
		t.Error("the escalation must name the floor as its reason (-want +got)", diff)
	}
	// the floor LANDS in the decision — clank's gate never recorded one at all.
	// An auditor reads the number off the record.
	if diff := cmp.Diff(0.75, got.FloorApplied); diff != "" {
		t.Error("the applied floor must be recorded on the decision (-want +got)", diff)
	}
	if got.GrantedBand != "" {
		t.Errorf("an escalation grants nothing: GrantedBand=%q", got.GrantedBand)
	}
}

func TestEvaluate_HoldsTheAuthorityCeiling(t *testing.T) {
	t.Parallel()
	// calmPolicy: MaxBand["tier-1"] = act_reversible.
	cases := map[string]struct {
		gov         *proposal.GovernanceLevel
		wantVerdict decision.Verdict
		wantReasons []string
	}{
		"Evaluate escalates a request above the tier ceiling": {
			gov:         &proposal.GovernanceLevel{Band: string(decision.BandActDisruptive)},
			wantVerdict: decision.VerdictEscalate,
			wantReasons: []string{hiss.ReasonAuthorityCeiling},
		},
		"Evaluate approves a request exactly at the tier ceiling": {
			gov:         &proposal.GovernanceLevel{Band: string(decision.BandActReversible)},
			wantVerdict: decision.VerdictApproved,
		},
		"Evaluate treats an absent band as the lowest request, never elevated": {
			gov:         nil, // clank doesn't populate GovernanceLevel yet (D-3)
			wantVerdict: decision.VerdictApproved,
		},
		"Evaluate escalates a band it cannot parse": {
			gov:         &proposal.GovernanceLevel{Band: "sudo_everything"},
			wantVerdict: decision.VerdictEscalate, // can't grant what you can't parse
			wantReasons: []string{hiss.ReasonAuthorityCeiling},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ps := governedSet()
			ps.Proposals[0].GovernanceLevel = tc.gov

			got := decide(t, ps, calmPolicy())

			if diff := cmp.Diff(tc.wantVerdict, got.Verdict); diff != "" {
				t.Error("ceiling verdict wrong (-want +got)", diff)
			}
			if diff := cmp.Diff(tc.wantReasons, got.Reasons); diff != "" {
				t.Error("ceiling reasons wrong (-want +got)", diff)
			}
		})
	}
}

func TestEvaluate_VetoesACandidateWithNoReversalPath(t *testing.T) {
	t.Parallel()
	ps := governedSet()
	ps.Proposals[0].ReversalPath = nil // three of the four L3 mechanisms is not enough

	got := decide(t, ps, calmPolicy())

	if diff := cmp.Diff(decision.VerdictEscalate, got.Verdict); diff != "" {
		t.Error("an irreversible candidate can NEVER be auto-approved (-want +got)", diff)
	}
	if diff := cmp.Diff([]string{hiss.ReasonIrreversible}, got.Reasons); diff != "" {
		t.Error("the veto must say why (-want +got)", diff)
	}
}

func TestEvaluate_EscalatesInsideAFreezeWindowOnly(t *testing.T) {
	t.Parallel()
	window := func(from, until time.Duration) []hiss.Window {
		return []hiss.Window{{
			Name:  "q3-change-freeze",
			Start: frozenNow().Add(from), End: frozenNow().Add(until),
		}}
	}
	cases := map[string]struct {
		freezes     []hiss.Window
		wantVerdict decision.Verdict
		wantReasons []string
	}{
		"Evaluate escalates while a freeze window is open": {
			freezes:     window(-time.Hour, time.Hour), // now is inside
			wantVerdict: decision.VerdictEscalate,
			wantReasons: []string{hiss.ReasonFreezeWindow + ":q3-change-freeze"}, // the WINDOW'S NAME rides in the reason
		},
		"Evaluate approves once the freeze window has closed": {
			freezes:     window(-3*time.Hour, -time.Hour), // now is after
			wantVerdict: decision.VerdictApproved,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pol := calmPolicy()
			pol.FreezeWindows = tc.freezes

			got := decide(t, governedSet(), pol)

			if diff := cmp.Diff(tc.wantVerdict, got.Verdict); diff != "" {
				t.Error("freeze verdict wrong (-want +got)", diff)
			}
			if diff := cmp.Diff(tc.wantReasons, got.Reasons); diff != "" {
				t.Error("freeze reasons wrong (-want +got)", diff)
			}
		})
	}
}

func TestEvaluate_NeverMutatesTheSetAndNeverReRanks(t *testing.T) {
	t.Parallel()
	in := governedSet()
	// governedSet is BAITED for this claim: p2 carries HIGHER confidence
	// (0.91 > p1's 0.87) but clank recommended p1. A re-ranker would take
	// the bait; a governor permits or refuses what it was handed.

	got := decide(t, in, calmPolicy())

	if diff := cmp.Diff(governedSet(), in); diff != "" {
		t.Error("Evaluate mutated its input — I-7 violated; the set is clank's, read-only (-want +got)", diff)
	}
	if diff := cmp.Diff("p1", got.CandidateRef); diff != "" {
		t.Error("hiss re-ranked: p2's 0.91 is bait, not a mandate (-want +got)", diff)
	}
}

func TestEvaluate_RejectsInputThatIsNotFitToGovern(t *testing.T) {
	t.Parallel()
	cases := map[string]func(ps *proposal.Set){
		"Evaluate rejects a set with no gate result at all": func(ps *proposal.Set) {
			ps.Gate = nil
		},
		"Evaluate rejects a set whose gate did not pass": func(ps *proposal.Set) {
			ps.Gate = &proposal.GateResult{Passed: false, Reason: "evidence"}
		},
		"Evaluate rejects a set whose recommendation names no candidate": func(ps *proposal.Set) {
			ps.Recommended = "p9" // dangling ref — malformed, not merely ungated
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ps := governedSet()
			breakIt(&ps)

			got := decide(t, ps, calmPolicy())

			if diff := cmp.Diff(decision.VerdictRejected, got.Verdict); diff != "" {
				t.Error("input unfit to govern must be REJECTED, not escalated (-want +got)", diff)
			}
			if diff := cmp.Diff([]string{hiss.ReasonUngatedInput}, got.Reasons); diff != "" {
				t.Error("the rejection must be on the record (-want +got)", diff)
			}
		})
	}
}

func TestAuthority_LivePolicyApprovesAStampedReversibleAct(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "hiss", "policy.yaml")) //nolint:gosec
	if err != nil {
		t.Fatalf("read live policy: %v", err)
	}
	var pol hiss.Policy
	if err := yaml.Unmarshal(raw, &pol); err != nil {
		t.Fatalf("unmarshal live policy: %v", err)
	}

	ps := proposal.Set{
		Name: "ps-node-death-001", SignalRef: "slo_burn:ceph-cluster",
		FailureClass: proposal.ClassResourceExhaustion, ServiceTier: "tier-1",
		Evidence: []proposal.EvidenceRef{{Tool: "metrics", Query: "ceph_health", Live: true}},
		Gate:     &proposal.GateResult{BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true},
		Proposals: []proposal.Candidate{{
			ID: "p1", ContractRef: "hold-rebalance", Confidence: 0.9,
			ReversalPath:    &proposal.ReversalPath{Method: "release-rebalance"},
			GovernanceLevel: &proposal.GovernanceLevel{Band: string(decision.BandActReversible)},
		}},
		Recommended: "p1", Status: &proposal.Status{Phase: "proposed"},
	}

	var auth hiss.Authority
	dec := auth.Evaluate(ps, pol, time.Now())

	if diff := cmp.Diff(decision.VerdictApproved, dec.Verdict); diff != "" {
		t.Fatalf("live policy must admit a stamped reversible hold-rebalance (-want +got)\n%s\nreasons: %v", diff, dec.Reasons)
	}
	if diff := cmp.Diff(decision.BandActReversible, dec.GrantedBand); diff != "" {
		t.Error("granted band should mirror the requested act_reversible band (-want +got)", diff)
	}
}

func TestEvaluate_ShaperHoldsHighBlastReversibleActions(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		reversible  bool
		blast       proposal.BlastTier
		wantVerdict decision.Verdict
		wantReasons []string
	}{
		"Evaluate auto-fires a low-blast reversible action": {
			reversible: true, blast: proposal.BlastLow,
			wantVerdict: decision.VerdictApproved,
		},
		"Evaluate holds a high-blast reversible action for a human": {
			reversible: true, blast: proposal.BlastHigh,
			wantVerdict: decision.VerdictHold, wantReasons: []string{decision.ReasonRiskCeiling},
		},
		"Evaluate escalates an irreversible action before the shaper ever runs": {
			// the gate's ReasonIrreversible veto fires in stage 1 — the
			// shaper never sees this candidate at all. See the subtlety
			// note below; this case is what pins it.
			reversible: false, blast: proposal.BlastLow,
			wantVerdict: decision.VerdictEscalate, wantReasons: []string{hiss.ReasonIrreversible},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ps := governedSet()
			ps.Proposals[0].BlastTier = tc.blast
			if !tc.reversible {
				ps.Proposals[0].ReversalPath = nil
			}
			pol := calmPolicy()
			pol.AutoBand = map[string]decision.Band{"tier-1": decision.BandActReversible}

			got := decide(t, ps, pol)

			if diff := cmp.Diff(tc.wantVerdict, got.Verdict); diff != "" {
				t.Error("shaper verdict wrong (-want +got)", diff)
			}
			if diff := cmp.Diff(tc.wantReasons, got.Reasons); diff != "" {
				t.Error("shaper reasons wrong (-want +got)", diff)
			}
		})
	}
}

func TestEvaluate_ShaperNeverRunsOnAnUngatedSet(t *testing.T) {
	t.Parallel()
	ps := governedSet()
	ps.Proposals[0].ReversalPath = nil // trips the stage-1 irreversibility veto

	got := decide(t, ps, calmPolicy())

	if diff := cmp.Diff(decision.VerdictEscalate, got.Verdict); diff != "" {
		t.Error("wrong verdict (-want +got)", diff)
	}
	if got.RiskBand != "" {
		t.Errorf("a stage-1 veto must short-circuit before the shaper runs: RiskBand=%q", got.RiskBand)
	}
}

func decide(t *testing.T, ps proposal.Set, pol hiss.Policy) decision.Decision {
	t.Helper()
	var auth hiss.Authority
	got := auth.Evaluate(ps, pol, frozenNow())
	if err := got.Auditable(); err != nil {
		t.Fatal("Evaluate produced an unauditable decision:", err)
	}
	return got
}

func governedSet() proposal.Set {
	return proposal.Set{
		Name:         "ps-ceph-rgw-001",                  // → Decision.ProposalRef
		SignalRef:    "slo_burn:ceph-rgw",                // rattle's fingerprint, threaded through clank → Decision.SignalRef
		FailureClass: proposal.ClassDependencySaturation, // Floors lookup key (with ServiceTier)
		ServiceTier:  "tier-1",                           // Floors + MaxBand lookup key
		Evidence: []proposal.EvidenceRef{{ // live — the gate's defence-5 leg was satisfied in clank
			Tool: "metrics", Query: "burn", Summary: "rgw pool saturating", Ref: "metrics://rgw", Live: true,
		}},
		Gate: &proposal.GateResult{ // clank only delivers passed sets; Claim 8 covers the "shouldn't happen"
			BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true,
		},
		Proposals: []proposal.Candidate{
			{ // p1 — the RECOMMENDED one: clears calmPolicy on every axis
				ID: "p1", ContractRef: "throttle-non-critical-paths",
				Confidence: 0.87, // ≥ the 0.75 floor (Claim 3 lowers this)
				ReversalPath: &proposal.ReversalPath{ // present — the I-12 veto stays quiet (Claim 5 nils this)
					Method: "unthrottle", Watching: "latency_p99", Trigger: "slo_recovery",
				},
				Rank: 1, // GovernanceLevel deliberately nil — D-3; Claim 4 pins absence-is-lowest
			},
			{ // p2 — BAIT for Claim 7: higher confidence, NOT recommended, no reversal.
				// A re-ranker would grab it; hiss must never look at it twice.
				ID: "p2", ContractRef: "restart-rgw-pool", Confidence: 0.91, Rank: 2,
			},
		},
		Recommended: "p1", // clank's call. hiss permits or refuses THIS, only this.
		Status:      &proposal.Status{Phase: "proposed"},
	}
}

// calmPolicy is the policy humans wrote in calm conditions: permissive enough
// that governedSet() approves, strict enough that every claim can trip one
// rule by breaking one field. NO fake policy engine — Policy is data;
// construct it inline per test when a claim needs a variant.
func calmPolicy() hiss.Policy {
	return hiss.Policy{
		Version: "v1",
		Floors: map[string]map[proposal.FailureClass]float64{
			"tier-1": {proposal.ClassDependencySaturation: 0.75},
		},
		MaxBand:         map[string]decision.Band{"tier-1": decision.BandActReversible},
		AutoBand:        map[string]decision.Band{"tier-1": decision.BandActReversible},
		FreezeWindows:   nil,  // Claim 6 adds one
		RequireReversal: true, // prod posture, always (I-12)
	}
}
