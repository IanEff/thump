// Package proposal is the boundary vocabulary of the Reasoning Plane: the
// ProposalSet and the value objects that ride it, shared by the beat that
// produces them (clank) and the beat that reads them (hiss). It is a leaf —
// time and signal only, an invariant pinned by leaf_test.go — so no beat
// imports another beat's internals to speak the contract.
//
// v1 is additive-only: never rename, retype, or repurpose a field here, since
// other processes (not just other packages) depend on this exact shape.
package proposal

import "time"

// Set is the ProposalSet — unstuttered to proposal.Set the same way rattle's
// SignalDetection became signal.Detection when it crossed into its leaf. The
// set, not the Recommended candidate alone, is the audit unit: "why X?"
// answers as "considered N actions, ranked them thus," never as a bare choice.
type Set struct {
	Name             string            `json:"name,omitempty" yaml:"name,omitempty"`
	SignalRef        string            `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`       // the originating signal.Detection's Fingerprint — an open Set sharing this value suppresses a new one (dedup)
	SAOSnapshot      *SAO              `json:"saoSnapshot,omitempty" yaml:"saoSnapshot,omitempty"`   // the SAO the reason loop actually reasoned over, frozen at emit time — Version > 0 or the audit trail is dangling
	FailureClass     FailureClass      `json:"failureClass,omitempty" yaml:"failureClass,omitempty"` // the model's leading hypothesis — never a rules-table lookup
	CausalScores     []CausalScore     `json:"causalScores,omitempty" yaml:"causalScores,omitempty"`
	Hypotheses       []Hypothesis      `json:"hypotheses,omitempty" yaml:"hypotheses,omitempty"`   // the leading and competing explanations, weighted — the reasoning chain, not just the winner
	Evidence         []EvidenceRef     `json:"evidence,omitempty" yaml:"evidence,omitempty"`       // digests only, gathered by the reason loop's tools — a Set with no Live entry among these fails the gate (belief-formation defence 5)
	ServiceTier      string            `json:"serviceTier,omitempty" yaml:"serviceTier,omitempty"` // copied from the originating signal.Detection; also what hiss's Authority indexes its Policy.Floors and Policy.MaxBand by
	Gate             *GateResult       `json:"gate,omitempty" yaml:"gate,omitempty"`               // budget ∧ dedup ∧ evidence — a Set with Gate == nil or Gate.Passed == false is rejected by hiss outright (ReasonUngatedInput)
	Proposals        []Candidate       `json:"proposals,omitempty" yaml:"proposals,omitempty"`     // every candidate action considered, ranked — not just the one recommended
	Recommended      string            `json:"recommended,omitempty" yaml:"recommended,omitempty"` // the rank-1 Candidate's ID; must resolve to an entry in Proposals or hiss rejects the Set
	RankingRationale *RankingRationale `json:"rankingRationale,omitempty" yaml:"rankingRationale,omitempty"`
	Status           *Status           `json:"status,omitempty" yaml:"status,omitempty"`
}

// ContractRefFor returns the ContractRef of the Proposals entry whose ID
// matches candidateID, or "" if none matches — the lookup every downstream
// beat needs to log which catalogued action a verdict concerns, without
// re-deriving what Recommended or a Decision's CandidateRef points at three
// separate times.
func (s Set) ContractRefFor(candidateID string) string {
	for _, c := range s.Proposals {
		if c.ID == candidateID {
			return c.ContractRef
		}
	}
	return ""
}

// RankingRationale records why the ranker ordered Proposals the way it did —
// the deterministic, auditable half of ranking, kept separate from the
// model's own hypothesis reasoning.
type RankingRationale struct {
	DominantAxis   string `json:"dominantAxis,omitempty" yaml:"dominantAxis,omitempty"`     // which axis decided the order, e.g. "time_to_effect"
	VelocityWeight string `json:"velocityWeight,omitempty" yaml:"velocityWeight,omitempty"` // the BlastRadius.Velocity reading that triggered velocity-weighting, e.g. "accelerating"
}

// Status is the Set's lifecycle state — set and read by the ledger, not the
// reason loop itself.
type Status struct {
	Phase        string    `json:"phase,omitempty" yaml:"phase,omitempty"` // one of the Phase* constants below
	Reason       string    `json:"reason,omitempty" yaml:"reason,omitempty"`
	SupersededBy string    `json:"supersededBy,omitempty" yaml:"supersededBy,omitempty"` // the Set.Name that superseded this one, when Phase is PhaseSuperseded
	Outcome      string    `json:"outcome,omitempty" yaml:"outcome,omitempty"`           // applied | success | failure | unknown | partial_non_converging — applied is interim (Phase stays Acted, awaiting convergence); the rest are terminal
	ObservedAt   time.Time `json:"observedAt,omitempty" yaml:"observedAt,omitempty"`
}

// Phase* enumerate Status.Phase. Dedup only suppresses against the open
// phases (PhaseProposed, PhaseAcknowledge, PhaseActed) — a closed Set can
// never suppress a live one.
const (
	PhaseProposed        = "proposed"
	PhaseAcknowledge     = "acknowledge"
	PhaseActed           = "acted"
	PhaseSuperseded      = "superseded"
	PhaseClosed          = "closed"
	PhaseNoAction        = "no_action"
	PhaseBudgetExhausted = "budget_exhausted"
	// PhaseDeclined means governance ruled against this Set before thump
	// ever rendered it — distinct from PhaseNoAction (clank itself never
	// proposed) and PhaseClosed (thump rendered/executed and a real
	// Outcome closed the loop). A declined Set never produces an Outcome,
	// so nothing here ever touches the case base.
	PhaseDeclined = "declined"
)

// Hypothesis is one candidate explanation for the FailureClass, weighted
// against the others the model considered — the reasoning chain, not just
// the leading guess.
type Hypothesis struct {
	Name   string  `json:"name" yaml:"name"`
	Weight float64 `json:"weight" yaml:"weight"`
}

// EvidenceRef is a digest of one read-only tool call, never the raw payload —
// there is no Raw field here, and there never will be; raw data cannot enter
// the reason loop's conversation history.
type EvidenceRef struct {
	Tool    string `json:"tool,omitempty" yaml:"tool,omitempty"`       // which tool produced this, e.g. "kube", "loki", "casebase"
	Query   string `json:"query,omitempty" yaml:"query,omitempty"`     // the exact query issued, for re-running, not for replaying results
	Summary string `json:"summary,omitempty" yaml:"summary,omitempty"` // the one-line digest that actually enters the conversation
	Ref     string `json:"ref,omitempty" yaml:"ref,omitempty"`         // a backend pointer to re-fetch the source data, e.g. "kube://ns/pods" — not the data itself
	Live    bool   `json:"live,omitempty" yaml:"live,omitempty"`       // true only for fresh telemetry, never for case-base/change-snapshot lookups — the gate requires at least one Live EvidenceRef to pass (belief-formation defence 5)
}

// Candidate is one catalogued action the reason loop proposed, with its own
// hypothesis confidence and governance band — a graded request, never a
// verdict; hiss decides whether it may proceed.
type Candidate struct {
	ID              string           `json:"id,omitempty" yaml:"id,omitempty"`
	ContractRef     string           `json:"contractRef,omitempty" yaml:"contractRef,omitempty"` // must name an entry in the ActionContract catalog — the engine rejects any Candidate whose ContractRef the catalog doesn't list
	Confidence      float64          `json:"confidence,omitempty" yaml:"confidence,omitempty"`   // the model's hypothesis confidence ("how sure of this fix?") — never signal.Divergence.Confidence ("is this real?")
	PredictedImpact *PredictedImpact `json:"predictedImpact,omitempty" yaml:"predictedImpact,omitempty"`
	BlastTier       BlastTier        `json:"blastTier,omitempty" yaml:"blastTier,omitempty"`             // authored; copied from the ActionContract at enrichment — hiss's shaper reads this for risk, never the effectiveness forecast in PredictedImpact
	ReversalPath    *ReversalPath    `json:"reversalPath,omitempty" yaml:"reversalPath,omitempty"`       // nil means the catalog's ActionContract has no reversal — hiss's irreversibility veto (ReasonIrreversible) reads exactly this absence
	GovernanceLevel *GovernanceLevel `json:"governanceLevel,omitempty" yaml:"governanceLevel,omitempty"` // nil is read as the lowest band (BandObserve), never as elevated privilege
	Rank            int              `json:"rank,omitempty" yaml:"rank,omitempty"`                       // 1-indexed position after ranking; rank 1 is what Set.Recommended names
}

// PredictedImpact is a forecast of what this Candidate would do to the
// signal, not a measured result — Outcome (api/v1/outcome) carries what
// actually happened, once thump acts.
type PredictedImpact struct {
	// SeverityReductionPct is the predicted cut to the signal's 0..1
	// error-budget severity — the authored per-action baseline stamped from
	// the ActionContract at enrichment, scored against the observed reduction
	// as agent_action_effectiveness_delta.
	SeverityReductionPct float64           `json:"severityReductionPct,omitempty" yaml:"severityReductionPct,omitempty"`
	BlastRadiusDelta     float64           `json:"blastRadiusDelta,omitempty" yaml:"blastRadiusDelta,omitempty"`
	SLOEffects           map[string]string `json:"sloEffects,omitempty" yaml:"sloEffects,omitempty"` // e.g. "time_to_effect" -> a duration string; the ranker reads this key when velocity-weighting
}

// ReversalPath is the catalog's scaffolding for undoing this Candidate's
// action, copied in from the ActionContract at enrichment time — its
// presence is what earns a Candidate the act_reversible band instead of
// act_disruptive.
type ReversalPath struct {
	Method   string `json:"method,omitempty" yaml:"method,omitempty"`
	Watching string `json:"watching,omitempty" yaml:"watching,omitempty"` // the metric to watch for reversal's own success criteria
	Trigger  string `json:"trigger,omitempty" yaml:"trigger,omitempty"`   // the target value that fires the reversal
}

// GovernanceLevel is a request, not a verdict — clank emits the band and
// stops; converting it to allow/deny is hiss's job, not clank's.
type GovernanceLevel struct {
	Band             string  `json:"band,omitempty" yaml:"band,omitempty"`                         // one of decision.Band's values (observe | act_reversible | act_disruptive) — duplicated here as a string, not the typed decision.Band, so this leaf package never imports decision's
	ThresholdApplied float64 `json:"thresholdApplied,omitempty" yaml:"thresholdApplied,omitempty"` // reserved for the deferred risk shaper (CRS) — never set in v1
}

// FailureClass is the model's leading hypothesis for what kind of failure
// this is — a closed enum, not an open string, and never a rules-table
// lookup; the model chooses among the Class* constants below.
type FailureClass string

const (
	ClassDependencySaturation FailureClass = "dependency_saturation"
	ClassTrafficShift         FailureClass = "traffic_shift"
	ClassResourceExhaustion   FailureClass = "resource_exhaustion"
	ClassRedundancyDegraded   FailureClass = "redundancy_degraded"
	ClassUnknown              FailureClass = "unknown"
)

// GateResult is a conjunction of minimums, never an average — one failing
// dimension (BudgetOK, DedupeOK, or EvidenceOK) fails Passed, and Reason
// names which one.
type GateResult struct {
	BudgetOK   bool   `json:"budgetOK,omitempty" yaml:"budgetOK,omitempty"`     // decision/error-budget headroom — distinct from the reason loop's own MaxSteps budget; always true in v1, the real check is unimplemented
	DedupeOK   bool   `json:"dedupeOK,omitempty" yaml:"dedupeOK,omitempty"`     // false when an open Set already exists for the same SignalRef
	EvidenceOK bool   `json:"evidenceOK,omitempty" yaml:"evidenceOK,omitempty"` // false unless at least one EvidenceRef.Live is true (belief-formation defence 5)
	Passed     bool   `json:"passed,omitempty" yaml:"passed,omitempty"`         // BudgetOK && DedupeOK && EvidenceOK — never a weighted or averaged score
	Reason     string `json:"reason,omitempty" yaml:"reason,omitempty"`         // which minimum failed, set only when Passed is false
}

// CausalScore is one change event's causal likelihood, decomposed and
// explained — the scorer that produces it enforces the belief-formation
// defences (≥2-source corroboration, freshness-decay, negative-signal
// checks) so a Candidate's confidence is earned, not decorative.
type CausalScore struct {
	EventID          string   `json:"eventID,omitempty" yaml:"eventID,omitempty"`                   // the ChangeEvent this score explains
	Temporal         float64  `json:"temporal,omitempty" yaml:"temporal,omitempty"`                 // recency component — decays with the event's Age
	Topological      float64  `json:"topological,omitempty" yaml:"topological,omitempty"`           // nonzero only when the event's Target sits in-path and degraded
	Historical       float64  `json:"historical,omitempty" yaml:"historical,omitempty"`             // the case-base prior, discounted by freshness-decay (defence 2)
	LiveCorroborated bool     `json:"liveCorroborated,omitempty" yaml:"liveCorroborated,omitempty"` // true only when live topology independently backs the historical match — required before history alone may raise Likelihood (defence 1)
	Likelihood       float64  `json:"likelihood,omitempty" yaml:"likelihood,omitempty"`             // capped well below 1.0 whenever LiveCorroborated is false; decremented, never left alone, by each absent PredictedSignal (defence 3)
	Rationale        []string `json:"rationale,omitempty" yaml:"rationale,omitempty"`               // human-legible evidence per score component, one line per defence applied
}

type BlastTier string

const (
	BlastLow  BlastTier = "low"  // node/pod-local, additive (e.g. add a replica)
	BlastMed  BlastTier = "med"  // service- or cluster-scoped but bounded/brief
	BlastHigh BlastTier = "high" // wide, hard-to-contain, or slow to bleed off
)
