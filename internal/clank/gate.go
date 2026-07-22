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
// both Live and topologically coherent — the forced live-telemetry citation
// defence, widened to the cross-domain axis: a live metric about a node the
// signal's own SAO has no declared relationship to cannot drive a
// classification on its own, the same "not alone" shape defence 1 already
// applies to an uncorroborated case-base match.
func (g ReadinessGate) Evaluate(ps proposal.Set, openDupes []proposal.Set) GateResult {
	budgetOK := true
	dedupeOK := len(openDupes) == 0
	evidenceOK := anyCoherentLive(recommendedEvidence(ps), ps.SAOSnapshot)

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

// anyCoherentLive reports whether refs has a Live entry the gate may treat
// as grounding on its own. A ref with no declared Subject makes no
// topology claim and always qualifies — this is what keeps every
// unclassified tool call (the whole fleet, until evidence-queries.yaml
// grows subject: tags) behaving exactly as it did before this check
// existed. A ref that does declare a Subject only qualifies when that node
// appears in sao's Topology; a cross-domain claim still enters Evidence
// (the model saw it, and it can corroborate a hypothesis another in-topology
// ref already grounds) but can't be the sole citation that clears the gate.
func anyCoherentLive(refs []proposal.EvidenceRef, sao *proposal.SAO) bool {
	for _, ref := range refs {
		if ref.Live && (ref.Subject == "" || inTopology(ref.Subject, sao)) {
			return true
		}
	}
	return false
}

// inTopology reports whether subject names a node in sao's frozen
// Topology snapshot — a nil sao (no SAO assembled) can confirm nothing, so
// it reports false rather than passing a claim it has no basis to check.
func inTopology(subject string, sao *proposal.SAO) bool {
	if sao == nil {
		return false
	}
	for _, group := range [][]proposal.NodeState{sao.Topology.Upstream, sao.Topology.Downstream} {
		for _, n := range group {
			if n.Name == subject {
				return true
			}
		}
	}
	return false
}

func recommendedEvidence(ps proposal.Set) []proposal.EvidenceRef {
	// fall back to ps.Evidence if no recommendations exist.
	if ps.Recommended == "" && len(ps.Proposals) == 0 {
		return ps.Evidence
	}
	var rec *proposal.Candidate
	for i := range ps.Proposals {
		if ps.Proposals[i].ID == ps.Recommended {
			rec = &ps.Proposals[i]
			break
		}
	}
	if rec == nil || len(rec.Citations) == 0 {
		return nil
	}

	cited := make(map[string]bool, len(rec.Citations))
	for _, c := range rec.Citations {
		cited[c] = true
	}

	var result []proposal.EvidenceRef
	for _, ref := range ps.Evidence {
		if cited[ref.Query] {
			result = append(result, ref)
		}
	}
	return result
}
