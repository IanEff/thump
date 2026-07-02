// Package clank is the Reasoning Plane: it turns one Signal into a ranked,
// deduplicated, evidence-backed ProposalSet. It selects; it does not permit,
// detect, or touch infrastructure.
package clank

import "github.com/ianeff/clank/internal/proposal"

// The ProposalSet vocabulary moved to internal/proposal (hiss Wave 1) so the
// Governance beat can read it without importing the reasoner. Aliases, not
// new types — every consumer is unchanged; burn these in their own commit.
type (
	ProposalSet      = proposal.Set
	RankingRationale = proposal.RankingRationale
	ProposalStatus   = proposal.Status
	Hypothesis       = proposal.Hypothesis
	EvidenceRef      = proposal.EvidenceRef
	Candidate        = proposal.Candidate
	PredictedImpact  = proposal.PredictedImpact
	ReversalPath     = proposal.ReversalPath
	GovernanceLevel  = proposal.GovernanceLevel
	FailureClass     = proposal.FailureClass
)

const (
	ClassDependencySaturation = proposal.ClassDependencySaturation
	ClassTrafficShift         = proposal.ClassTrafficShift
	ClassResourceExhaustion   = proposal.ClassResourceExhaustion
	ClassUnknown              = proposal.ClassUnknown
)
