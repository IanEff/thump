package thump_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/publish"
	"github.com/ianeff/thump/internal/thump"
	"sigs.k8s.io/yaml"
)

func goldenOutcome() outcome.Outcome {
	return outcome.Outcome{
		ID:          "out:slo_burn:ceph-rgw:1000",
		DecisionRef: "dec:slo_burn:ceph-rgw:1000",
		SignalRef:   "slo_burn:ceph-rgw",
		ContractRef: "throttle-non-critical-paths",
		Mode:        outcome.ModeDryRun,     // the honest half…
		Result:      outcome.ResultRendered, // …of the honest whole: we rehearsed, we did not act
		Error:       "",
		ExecutedAt:  frozenNow(),
	}
}

func withoutDecisionRef(o outcome.Outcome) outcome.Outcome {
	o.DecisionRef = ""
	return o
}

func withoutExecutedAt(o outcome.Outcome) outcome.Outcome {
	o.ExecutedAt = time.Time{}
	return o
}

func withoutMode(o outcome.Outcome) outcome.Outcome {
	o.Mode = ""
	return o
}

func silentFailure(o outcome.Outcome) outcome.Outcome {
	o.Result, o.Error = outcome.ResultFailure, ""
	return o
}

func explainedPartial(o outcome.Outcome) outcome.Outcome {
	o.Result, o.Error = outcome.ResultPartialNonConverging, "latency recovered; error rate did not"
	return o
}

func silentPartial(o outcome.Outcome) outcome.Outcome {
	o.Result, o.Error = outcome.ResultPartialNonConverging, ""
	return o
}

func frozenNow() time.Time { return time.Unix(1000, 0) }

func approvedGoverned() decision.Governed {
	set := proposal.Set{
		Name:         "ps-ceph-rgw-001",   // → Decision.ProposalRef (Claim 6 breaks this)
		SignalRef:    "slo_burn:ceph-rgw", // rattle's fingerprint → Decision.SignalRef → Order → Outcome
		FailureClass: proposal.ClassDependencySaturation,
		ServiceTier:  "tier-1",
		Evidence: []proposal.EvidenceRef{{
			Tool: "metrics", Query: "burn", Summary: "rgw pool saturating", Ref: "metrics://rgw", Live: true,
		}},
		Gate: &proposal.GateResult{ // hiss only approves gated sets; the envelope carries the proof
			BudgetOK: true, DedupeOK: true, EvidenceOK: true, Passed: true,
		},
		Proposals: []proposal.Candidate{
			{ // p1 — the RECOMMENDED and GRANTED one
				ID: "p1", ContractRef: "throttle-non-critical-paths", // → richCatalog lookup (Claim 7 breaks this)
				Confidence: 0.87,
				ReversalPath: &proposal.ReversalPath{ // hiss's I-12 veto guaranteed this exists on any prod approval
					Method: "unthrottle", Watching: "latency_p99", Trigger: "slo_recovery",
				},
				GovernanceLevel: &proposal.GovernanceLevel{Band: "act_reversible"}, // the upgrade over hiss's fixture
				Rank:            1,
			},
			{ // p2 — inherited re-ranker bait: higher confidence, NOT granted.
				// thump must never look at it at all (Claim 8's cmp.Diff would
				// catch a "helpful" substitution as a mutation… and Claim 9's
				// golden ContractRef pins it from the other side).
				ID: "p2", ContractRef: "restart-rgw-pool", Confidence: 0.91, Rank: 2,
			},
		},
		Recommended: "p1",
		Status:      &proposal.Status{Phase: "proposed"},
	}
	return decision.Governed{
		Decision: decision.Decision{
			ID:            "dec:slo_burn:ceph-rgw:1000", // "dec:" + SignalRef + ":" + frozenNow().Unix() — hiss's stamp
			ProposalRef:   "ps-ceph-rgw-001",            // set.Name
			SignalRef:     "slo_burn:ceph-rgw",          // set.SignalRef, untouched
			CandidateRef:  "p1",                         // set.Recommended — I-7, hiss never re-ranked
			Verdict:       decision.VerdictApproved,     // every rule stayed quiet
			Reasons:       nil,                          // approval needs no excuse
			RequestedBand: decision.BandActReversible,   // p1's GovernanceLevel (the D-3 upgrade)
			GrantedBand:   decision.BandActReversible,   // == RequestedBand on approval
			FloorApplied:  0.75,                         // the policy floor hiss checked and recorded
			PolicyVersion: "v1",
			EvaluatedAt:   frozenNow(),
		},
		Set: set,
	}
}

// escalatedGoverned is a WELL-FORMED escalation (auditable: it has reasons)
// — Claim 14's transport input. Not a mutator on the Decision alone: the
// envelope stays coherent, only the verdict changes.
func escalatedGoverned() decision.Governed {
	g := approvedGoverned()
	g.Decision.Verdict = decision.VerdictEscalate
	g.Decision.Reasons = []string{decision.ReasonConfidenceFloor}
	g.Decision.GrantedBand = ""
	return g
}

// richCatalog is the catalog a human authored in calm conditions — the
// throttle contract with every execution-relevant field populated. Catalog
// is DATA: construct variants inline when a claim needs one.
func richCatalog() *contract.StaticCatalog {
	return contract.NewStaticCatalog([]contract.ActionContract{{
		Name:                     "throttle-non-critical-paths",
		ApplicableFailureClasses: []proposal.FailureClass{proposal.ClassDependencySaturation},
		ApplicableTiers:          []string{"tier-1"},
		Action: contract.ActionSpec{
			Description: "Throttle non-critical request paths at the RGW ingress",
			ScopeParameters: map[string]contract.Range{
				"throttle_pct": {Min: 10, Max: 60, Default: 25}, // Default → Order.Parameters (Claim 9)
			},
		},
		Reversal: contract.Reversal{
			Method:   "unthrottle",
			Fallback: "page-oncall", // → ReversalPlan.Fallback — the authored escape hatch
		},
		SuccessCriteria: contract.SuccessCriteria{
			Metric:          "latency_p99",
			Target:          "p99 < 250ms",
			Window:          10 * time.Minute, // rendered, not evaluated, in v1 (PARKED: the convergence watcher)
			AbortConditions: []string{"error_rate > 2%"},
		},
	}})
}

func goldenOrder() thump.Order {
	return thump.Order{
		ID:          "ord:slo_burn:ceph-rgw:1000",
		DecisionRef: "dec:slo_burn:ceph-rgw:1000",
		SignalRef:   "slo_burn:ceph-rgw",
		ContractRef: "throttle-non-critical-paths", // p1's, never p2's — I-7 inherited
		GrantedBand: decision.BandActReversible,
		Description: "Throttle non-critical request paths at the RGW ingress",
		Parameters:  map[string]float64{"throttle_pct": 25}, // Range.Default — I-4
		Reversal: thump.ReversalPlan{
			Method: "unthrottle", Watching: "latency_p99", Trigger: "slo_recovery",
			Fallback: "page-oncall",
		},
		Success: contract.SuccessCriteria{
			Metric: "latency_p99", Target: "p99 < 250ms",
			Window: 10 * time.Minute, AbortConditions: []string{"error_rate > 2%"},
		},
		RenderedAt: frozenNow(),
	}
}

// writeGovernedYAML / readOneOrder / readOneOutcome — the transport
// round-trip via sigs.k8s.io/yaml, the codec clank's sink already writes
// with — the test round-trip is the real round-trip.
func writeGovernedYAML(t *testing.T, dir, name string, g decision.Governed) {
	t.Helper()
	out, err := yaml.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readOneOrder(t *testing.T, outbox string) thump.Order {
	t.Helper()
	var o thump.Order
	readOneYAML(t, filepath.Join(outbox, "orders"), &o)
	return o
}

func readOneOutcome(t *testing.T, outbox string) outcome.Outcome {
	t.Helper()
	var o outcome.Outcome
	readOneYAML(t, filepath.Join(outbox, "outcomes"), &o)
	return o
}

func readOneYAML(t *testing.T, dir string, out any) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("%s must hold exactly one file, found %d: %v", dir, len(matches), matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

// newTestTransport wires a Transport with the rich catalog, a fresh ledger,
// the dry-run executor, and the frozen clock — a constructor, so each test
// owns its state.
func newTestTransport(inbox, outbox string) *thump.Transport {
	return &thump.Transport{
		Inbox: inbox,
		OrderPub: &publish.DirPublisher[thump.Order]{
			Dir:  filepath.Join(outbox, "orders"),
			Name: func(o thump.Order) string { return o.SignalRef },
		},
		OutcomePub: &publish.DirPublisher[outcome.Outcome]{
			Dir:  filepath.Join(outbox, "outcomes"),
			Name: func(o outcome.Outcome) string { return o.SignalRef },
		},
		Catalog: richCatalog(),
		Log:     thump.NewOutcomeLog(),
		Exec:    thump.DryRun{},
		Now:     frozenNow,
	}
}

func TestTick_OrderAndOutcomeShareTheSignalRefName(t *testing.T) {
	inbox, outbox := t.TempDir(), t.TempDir()
	writeGovernedYAML(t, inbox, "gov-001.yaml", approvedGoverned())
	tr := newTestTransport(inbox, outbox)

	if err := tr.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	ref := approvedGoverned().Decision.SignalRef
	if _, err := os.Stat(filepath.Join(outbox, "orders", ref+".yaml")); err != nil {
		t.Errorf("order must be named by SignalRef: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outbox, "outcomes", ref+".yaml")); err != nil {
		t.Errorf("outcome must be named by SignalRef: %v", err)
	}
}
