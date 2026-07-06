package thump_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/thump"
)

func TestRender_RefusesEverythingButAnApproval(t *testing.T) {
	t.Parallel()
	cases := map[string]func(g *decision.Governed){
		"Render refuses an escalated decision": func(g *decision.Governed) {
			g.Decision.Verdict = decision.VerdictEscalate
			g.Decision.Reasons = []string{decision.ReasonConfidenceFloor}
			g.Decision.GrantedBand = ""
		},
		"Render refuses a rejected decision": func(g *decision.Governed) {
			g.Decision.Verdict = decision.VerdictRejected
			g.Decision.Reasons = []string{decision.ReasonUngatedInput}
			g.Decision.GrantedBand = ""
		},
		"Render refuses a decision with no verdict at all": func(g *decision.Governed) {
			g.Decision.Verdict = "" // still auditable (it carries reasons) — the verdict check must catch it
			g.Decision.Reasons = []string{decision.ReasonUngatedInput}
			g.Decision.GrantedBand = ""
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := approvedGoverned()
			breakIt(&g)

			_, err := thump.Actuator{}.Render(g, richCatalog(), frozenNow())
			if !errors.Is(err, thump.ErrUngoverned) {
				t.Errorf("a non-approval must earn ErrUngoverned, got %v", err)
			}
		})
	}
}

func TestRender_RefusesAnUnauditableDecision(t *testing.T) {
	t.Parallel()
	g := approvedGoverned()
	g.Decision.PolicyVersion = "" // one audit leg stripped -> Auditable() fails

	_, err := thump.Actuator{}.Render(g, richCatalog(), frozenNow())

	if !errors.Is(err, thump.ErrUnauditable) {
		t.Errorf("an unauditable decision must never render, got %v", err)
	}
}

func TestRender_RefusesAMismatchedPair(t *testing.T) {
	t.Parallel()
	cases := map[string]func(g *decision.Governed){
		"Render refuses a decision naming a different proposal set": func(g *decision.Governed) {
			g.Decision.ProposalRef = "ps-somebody-else"
		},
		"Render refuses a pair whose fingerprints disagree": func(g *decision.Governed) {
			g.Decision.SignalRef = "slo_burn:not-this-one"
		},
		"Render refuses a decision granting a candidate the set does not contain": func(g *decision.Governed) {
			g.Decision.CandidateRef = "p9" // dangling — nothing to render
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := approvedGoverned()
			breakIt(&g)

			_, err := thump.Actuator{}.Render(g, richCatalog(), frozenNow())

			if !errors.Is(err, thump.ErrSeamMismatch) {
				t.Errorf("a mismatched pair must earn ErrSeamMismatch, got %v", err)
			}
		})
	}
}

func TestRender_RefusesAContractOutsideTheCatalog(t *testing.T) {
	t.Parallel()
	g := approvedGoverned()
	g.Set.Proposals[0].ContractRef = "uncatalogued-mystery-action"

	_, err := thump.Actuator{}.Render(g, richCatalog(), frozenNow())

	if !errors.Is(err, thump.ErrOutsideCatalog) {
		t.Errorf("execution can't run what isn't catalogued, got %v", err)
	}
}

func TestRender_NeverMutatesTheGovernedPair(t *testing.T) {
	t.Parallel()
	in := approvedGoverned()

	if _, err := (thump.Actuator{}).Render(in, richCatalog(), frozenNow()); err != nil {
		t.Fatal("golden pair must render:", err)
	}

	if diff := cmp.Diff(approvedGoverned(), in); diff != "" {
		t.Error("Render mutated its input — the envelope is hiss's, read-only (-want +got)", diff)
	}
}

func TestRender_GoldenPath_RendersTheWholeOrder(t *testing.T) {
	t.Parallel()
	got, err := thump.Actuator{}.Render(approvedGoverned(), richCatalog(), frozenNow())
	if err != nil {
		t.Fatal("the golden pair must render:", err)
	}

	if diff := cmp.Diff(goldenOrder(), got); diff != "" {
		t.Error("rendered order drifted from the golden fixture (-want +got)", diff)
	}
}
