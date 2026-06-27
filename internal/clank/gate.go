package clank

type ReadinessGate struct{}

func (g ReadinessGate) Evaluate(ps ProposalSet, openDupes []ProposalSet, _ GatePolicy) GateResult {
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

type GateResult struct {
	BudgetOK         bool
	DedupeOK         bool
	EvidenceOK       bool
	ThresholdApplied float64
	Passed           bool
	Reason           string
}
