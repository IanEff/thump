// Package proposal is the boundary vocabulary of the Reasoning Plane: the
// ProposalSet and the value objects that ride it, shared by the beat that
// produces them (clank) and the beat that reads them (hiss). It is a leaf —
// time and internal/signal only, an invariant pinned by leaf_test.go — so no
// beat imports another beat's internals to speak the contract.
package proposal

import "time"

// Set is the ProposalSet — unstuttered to proposal.Set the same way rattle's
// SignalDetection became signal.Detection when it crossed into its leaf.
type Set struct {
	Name             string            `json:"name,omitempty"`
	SignalRef        string            `json:"signalRef,omitempty"`
	SAOSnapshot      *SAO              `json:"saoSnapshot,omitempty"`
	FailureClass     FailureClass      `json:"failureClass,omitempty"`
	CausalScores     []CausalScore     `json:"causalScores,omitempty"`
	Hypotheses       []Hypothesis      `json:"hypotheses,omitempty"`
	Evidence         []EvidenceRef     `json:"evidence,omitempty"`
	ServiceTier      string            `json:"serviceTier,omitempty"`
	Gate             *GateResult       `json:"gate,omitempty"`
	Proposals        []Candidate       `json:"proposals,omitempty"`
	Recommended      string            `json:"recommended,omitempty"`
	RankingRationale *RankingRationale `json:"rankingRationale,omitempty"`
	Status           *Status           `json:"status,omitempty"`
}

type RankingRationale struct {
	DominantAxis   string
	VelocityWeight string
}

type Status struct {
	Phase        string // proposed | acknowledge | acted | superseded | closed | no_action
	SupersededBy string
	Outcome      string // success | failure | unknown | partial_non_converging
	ObservedAt   time.Time
}

type Hypothesis struct {
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
}

type EvidenceRef struct {
	Tool    string
	Query   string
	Summary string
	Ref     string
	Live    bool
}

type Candidate struct {
	ID              string           `json:"id,omitempty"`
	ContractRef     string           `json:"contractRef,omitempty"`
	Confidence      float64          `json:"confidence,omitempty"`
	PredictedImpact *PredictedImpact `json:"predictedImpact,omitempty"`
	ReversalPath    *ReversalPath    `json:"reversalPath,omitempty"`
	GovernanceLevel *GovernanceLevel `json:"governanceLevel,omitempty"`
	Rank            int              `json:"rank,omitempty"`
}

type PredictedImpact struct {
	SeverityReductionPct float64
	BlastRadiusDelta     float64
	SLOEffects           map[string]string
}

type ReversalPath struct {
	Method   string
	Watching string
	Trigger  string
}

type GovernanceLevel struct {
	Band             string
	ThresholdApplied float64
}

type FailureClass string

const (
	ClassDependencySaturation FailureClass = "dependency_saturation"
	ClassTrafficShift         FailureClass = "traffic_shift"
	ClassResourceExhaustion   FailureClass = "resource_exhaustion"
	ClassUnknown              FailureClass = "unknown"
)

type GateResult struct {
	BudgetOK         bool
	DedupeOK         bool
	EvidenceOK       bool
	ThresholdApplied float64
	Passed           bool
	Reason           string
}

// CausalScore is one change event's causal likelihood, decomposed and explained.
// The scorer enforces the belief-formation defences (ch9 §7.7).
type CausalScore struct {
	EventID          string
	Temporal         float64
	Topological      float64
	Historical       float64
	LiveCorroborated bool
	Likelihood       float64
	Rationale        []string
}
