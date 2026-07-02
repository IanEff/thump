package clank

import "github.com/ianeff/clank/internal/proposal"

// The SAO aggregate moved to internal/proposal with the rest of the
// ProposalSet vocabulary (hiss Wave 1) — ProposalSet.SAOSnapshot carries it
// across the boundary, so it rides the same leaf.
type (
	SAO              = proposal.SAO
	SignalSnapshot   = proposal.SignalSnapshot
	TopologySnapshot = proposal.TopologySnapshot
	NodeState        = proposal.NodeState
	ChangeSnapshot   = proposal.ChangeSnapshot
	ChangeEvent      = proposal.ChangeEvent
)
