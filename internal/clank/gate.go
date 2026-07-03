package clank

import "github.com/ianeff/clank/internal/proposal"

// GateResult moved to internal/proposal (hiss Wave 1): hiss reads it off the
// ProposalSet. The gate that produces it stays here — behavior doesn't move.
type GateResult = proposal.GateResult

type ReadinessGate struct{}

func (g ReadinessGate) Evaluate(ps ProposalSet, openDupes []ProposalSet) GateResult {
	budgetOK := true
	dedupeOK := len(openDupes) == 0
	evidenceOK := anyLive(ps.Evidence)

	passed := budgetOK && dedupeOK && evidenceOK
	reason := ""

	if !passed {
		switch {
		case !evidenceOK:
			reason = "evidence"
		case !dedupeOK:
			reason = "dedupe"
		case !budgetOK:
			reason = "budget"
		}
	}
	return GateResult{
		BudgetOK:   budgetOK,
		DedupeOK:   dedupeOK,
		EvidenceOK: evidenceOK,
		Passed:     passed,
		Reason:     reason,
	}
}

func anyLive(refs []EvidenceRef) bool {
	for _, ref := range refs {
		if ref.Live {
			return true
		}
	}
	return false
}
