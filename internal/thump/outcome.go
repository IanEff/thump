package thump

import "github.com/ianeff/thump/api/v1/outcome"

// Aliases onto internal/outcome (Wave 1 extraction, fourth leaf) — kept so
// nothing else in thump has to change. Burn these in their own commit once
// every caller has migrated to the outcome package directly.
type (
	Outcome = outcome.Outcome
	Mode    = outcome.Mode
	Result  = outcome.Result
)

const (
	ModeDryRun = outcome.ModeDryRun
	ModeLive   = outcome.ModeLive

	ResultRendered             = outcome.ResultRendered
	ResultSuccess              = outcome.ResultSuccess
	ResultFailure              = outcome.ResultFailure
	ResultUnknown              = outcome.ResultUnknown
	ResultPartialNonConverging = outcome.ResultPartialNonConverging
)
