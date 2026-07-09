package clank

import "github.com/ianeff/thump/api/v1/proposal"

// GateResult is a conjunction of minimums, never an average — one failing
// dimension (BudgetOK, DedupeOK, EvidenceOK) fails the whole gate, so a
// confident set with zero live evidence still can't emit. It lives in
// api/v1/proposal so hiss can read it straight off a delivered proposal.Set;
// the ReadinessGate that produces it stays in clank, because its evidence
// leg is a belief-formation defence native to the reasoner, not a policy
// check.
type GateResult = proposal.GateResult

// ReadinessGate decides whether a formed proposal.Set is worth emitting — it
// never decides whether an action is authorized. It carries zero policy: no
// criticality tier, no error-budget check, no confidence threshold. Any of
// those belongs to hiss, the Governance Plane; adding one here is the seam
// that rots first.
type ReadinessGate struct{}

// Evaluate computes budget ∧ dedup ∧ evidence. BudgetOK is always true —
// the real budget is the engine's MaxSteps, already spent by the time
// Evaluate runs. DedupeOK is false when openDupes is non-empty: an open set
// for the same fingerprint suppresses a new one (suppressed means recorded,
// not delivered). EvidenceOK is false unless at least one ps.Evidence ref is
// Live — the forced live-telemetry citation defence: a set grounded only in
// change_snapshot or historical_alignment cannot pass.
func (g ReadinessGate) Evaluate(ps proposal.Set, openDupes []proposal.Set) GateResult {
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

func anyLive(refs []proposal.EvidenceRef) bool {
	for _, ref := range refs {
		if ref.Live {
			return true
		}
	}
	return false
}
