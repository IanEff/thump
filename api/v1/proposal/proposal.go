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
// SignalDetection became signal.Detection when it crossed into its leaf.
type Set struct {
	Name             string            `json:"name,omitempty" yaml:"name,omitempty"`
	SignalRef        string            `json:"signalRef,omitempty" yaml:"signalRef,omitempty"`
	SAOSnapshot      *SAO              `json:"saoSnapshot,omitempty" yaml:"saoSnapshot,omitempty"`
	FailureClass     FailureClass      `json:"failureClass,omitempty" yaml:"failureClass,omitempty"`
	CausalScores     []CausalScore     `json:"causalScores,omitempty" yaml:"causalScores,omitempty"`
	Hypotheses       []Hypothesis      `json:"hypotheses,omitempty" yaml:"hypotheses,omitempty"`
	Evidence         []EvidenceRef     `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	ServiceTier      string            `json:"serviceTier,omitempty" yaml:"serviceTier,omitempty"`
	Gate             *GateResult       `json:"gate,omitempty" yaml:"gate,omitempty"`
	Proposals        []Candidate       `json:"proposals,omitempty" yaml:"proposals,omitempty"`
	Recommended      string            `json:"recommended,omitempty" yaml:"recommended,omitempty"`
	RankingRationale *RankingRationale `json:"rankingRationale,omitempty" yaml:"rankingRationale,omitempty"`
	Status           *Status           `json:"status,omitempty" yaml:"status,omitempty"`
}

type RankingRationale struct {
	DominantAxis   string `json:"dominantAxis,omitempty" yaml:"dominantAxis,omitempty"`
	VelocityWeight string `json:"velocityWeight,omitempty" yaml:"velocityWeight,omitempty"`
}

type Status struct {
	Phase        string    `json:"phase,omitempty" yaml:"phase,omitempty"` // proposed | acknowledge | acted | superseded | closed | no_action
	Reason       string    `json:"reason,omitempty" yaml:"reason,omitempty"`
	SupersededBy string    `json:"supersededBy,omitempty" yaml:"supersededBy,omitempty"`
	Outcome      string    `json:"outcome,omitempty" yaml:"outcome,omitempty"` // success | failure | unknown | partial_non_converging
	ObservedAt   time.Time `json:"observedAt,omitempty" yaml:"observedAt,omitempty"`
}

const (
	PhaseProposed        = "proposed"
	PhaseAcknowledge     = "acknowledge"
	PhaseActed           = "acted"
	PhaseSuperseded      = "superseded"
	PhaseClosed          = "closed"
	PhaseNoAction        = "no_action"
	PhaseBudgetExhausted = "budget_exhausted" // engine.go:104 writes it; the enum comment never knew
)

type Hypothesis struct {
	Name   string  `json:"name" yaml:"name"`
	Weight float64 `json:"weight" yaml:"weight"`
}

type EvidenceRef struct {
	Tool    string `json:"tool,omitempty" yaml:"tool,omitempty"`
	Query   string `json:"query,omitempty" yaml:"query,omitempty"`
	Summary string `json:"summary,omitempty" yaml:"summary,omitempty"`
	Ref     string `json:"ref,omitempty" yaml:"ref,omitempty"`
	Live    bool   `json:"live,omitempty" yaml:"live,omitempty"`
}

type Candidate struct {
	ID              string           `json:"id,omitempty" yaml:"id,omitempty"`
	ContractRef     string           `json:"contractRef,omitempty" yaml:"contractRef,omitempty"`
	Confidence      float64          `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	PredictedImpact *PredictedImpact `json:"predictedImpact,omitempty" yaml:"predictedImpact,omitempty"`
	ReversalPath    *ReversalPath    `json:"reversalPath,omitempty" yaml:"reversalPath,omitempty"`
	GovernanceLevel *GovernanceLevel `json:"governanceLevel,omitempty" yaml:"governanceLevel,omitempty"`
	Rank            int              `json:"rank,omitempty" yaml:"rank,omitempty"`
}

type PredictedImpact struct {
	SeverityReductionPct float64           `json:"severityReductionPct,omitempty" yaml:"severityReductionPct,omitempty"`
	BlastRadiusDelta     float64           `json:"blastRadiusDelta,omitempty" yaml:"blastRadiusDelta,omitempty"`
	SLOEffects           map[string]string `json:"sloEffects,omitempty" yaml:"sloEffects,omitempty"`
}

type ReversalPath struct {
	Method   string `json:"method,omitempty" yaml:"method,omitempty"`
	Watching string `json:"watching,omitempty" yaml:"watching,omitempty"`
	Trigger  string `json:"trigger,omitempty" yaml:"trigger,omitempty"`
}

type GovernanceLevel struct {
	Band             string  `json:"band,omitempty" yaml:"band,omitempty"`
	ThresholdApplied float64 `json:"thresholdApplied,omitempty" yaml:"thresholdApplied,omitempty"`
}

type FailureClass string

const (
	ClassDependencySaturation FailureClass = "dependency_saturation"
	ClassTrafficShift         FailureClass = "traffic_shift"
	ClassResourceExhaustion   FailureClass = "resource_exhaustion"
	ClassUnknown              FailureClass = "unknown"
)

type GateResult struct {
	BudgetOK   bool   `json:"budgetOK,omitempty" yaml:"budgetOK,omitempty"`
	DedupeOK   bool   `json:"dedupeOK,omitempty" yaml:"dedupeOK,omitempty"`
	EvidenceOK bool   `json:"evidenceOK,omitempty" yaml:"evidenceOK,omitempty"`
	Passed     bool   `json:"passed,omitempty" yaml:"passed,omitempty"`
	Reason     string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// CausalScore is one change event's causal likelihood, decomposed and explained.
// The scorer enforces the belief-formation defences (ch9 §7.7).
type CausalScore struct {
	EventID          string   `json:"eventID,omitempty" yaml:"eventID,omitempty"`
	Temporal         float64  `json:"temporal,omitempty" yaml:"temporal,omitempty"`
	Topological      float64  `json:"topological,omitempty" yaml:"topological,omitempty"`
	Historical       float64  `json:"historical,omitempty" yaml:"historical,omitempty"`
	LiveCorroborated bool     `json:"liveCorroborated,omitempty" yaml:"liveCorroborated,omitempty"`
	Likelihood       float64  `json:"likelihood,omitempty" yaml:"likelihood,omitempty"`
	Rationale        []string `json:"rationale,omitempty" yaml:"rationale,omitempty"`
}
