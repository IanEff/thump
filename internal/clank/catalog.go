package clank

import "github.com/ianeff/clank/internal/contract"

// The action-contract catalog vocabulary moved to internal/contract (thump
// Wave 1b) — thump resolves a granted ContractRef from the same catalog
// without importing clank's internals. Aliases, not new types — every
// consumer is unchanged; burn these in their own commit.
type (
	ActionContract  = contract.ActionContract
	ActionSpec      = contract.ActionSpec
	Range           = contract.Range
	Reversal        = contract.Reversal
	SuccessCriteria = contract.SuccessCriteria
	Precondition    = contract.Precondition
	StaticCatalog   = contract.StaticCatalog
)

var (
	NewStaticCatalog  = contract.NewStaticCatalog
	ErrOutsideCatalog = contract.ErrOutsideCatalog
)
