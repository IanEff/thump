// Package clank is the Reasoning Plane: it turns one Signal into a recorded,
// deduplicated, evidence-backed Proposal. It selects; it does not permit, detect,
// or touch infrastructure.
package clank

import "time"

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
	Outcome      string // success | failure | unknown | PARTIAL_NON_CONVERGING
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

type SAO struct {
	Version     int
	AssembledAt time.Time
	Signal      SignalSnapshot
	Topology    TopologySnapshot
	Change      ChangeSnapshot
}

type SignalSnapshot struct {
	Confidence  float64
	Metric      string
	Severity    Severity
	BlastRadius BlastRadius
}

type TopologySnapshot struct {
	Upstream   []NodeState
	Downstream []NodeState
}

type NodeState struct {
	Name          string
	State         string
	DegradedSince time.Duration
	TrafficShare  float64
}

type ChangeSnapshot struct {
	Events []ChangeEvent
}

type ChangeEvent struct {
	ID     string
	Type   string // deploy | config | flag | rollback
	Target string
	Age    time.Duration

	CausalLikelihood float64  // 0 until scored
	Rationale        []string // human-legible evidence per score
}

type BlastRadius struct {
	AffectedPct         float64
	Velocity            string
	DownstreamConsumers int
}

type Severity struct {
	DegradationPct float64
	Trajectory     string
}

type FailureClass string

const (
	ClassDependencySaturation FailureClass = "dependency_saturation"
	ClassTrafficShift         FailureClass = "traffic_shift"
	ClassResourceExhaustion   FailureClass = "resource_exhaustion"
	ClassUnknown              FailureClass = "unknown"
)

type GatePolicy struct {
	Threshold     map[string]map[FailureClass]float64
	CausalWeights CausalWeights
}

type CausalWeights struct {
	Temporal    float64
	Topological float64
	Historical  float64
}

type GateResult struct {
	BudgetOK         bool
	DedupeOK         bool
	EvidenceOK       bool
	ThresholdApplied float64
	Passed           bool
	Reason           string
}
