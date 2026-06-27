package clank

import "time"

type ProposalSet struct {
	Name             string
	SignalRef        string
	SAOSnapshot      SAO
	FailureClass     FailureClass
	Hypotheses       []Hypothesis
	Evidence         []EvidenceRef
	ServiceTier      string
	Gate             GateResult
	Proposals        []Candidate
	Recommended      string
	RankingRationale RankingRationale
	Status           ProposalStatus
}

type RankingRationale struct {
	DominantAxis   string
	VelocityWeight string
}

type ProposalStatus struct {
	Phase        string // proposed | acknowledge | acted | superseded | closed | no_action
	SupersededBy string
	Outcome      string // success | failure | unknown | partial_non_converging
	ObservedAt   time.Time
}

type Hypothesis struct {
	Name   string
	Weight float64
}

type EvidenceRef struct {
	Tool    string
	Query   string
	Summary string
	Ref     string
	Live    bool
}

type Candidate struct {
	ID              string
	ContractRef     string
	Confidence      float64
	PredictedImpact PredictedImpact
	ReversalPath    ReversalPath
	GovernanceLevel GovernanceLevel
	Rank            int
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
