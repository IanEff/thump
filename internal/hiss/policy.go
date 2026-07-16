package hiss

import (
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

// Window is one freeze period during which Authority.Evaluate escalates
// every request regardless of confidence or band — a named change freeze,
// not a general-purpose throttle. The interval is half-open: now in
// [Start, End) is inside the window, now == End is not.
type Window struct {
	Name  string    `json:"name,omitempty" yaml:"name,omitempty"` // operator label, appended to ReasonFreezeWindow so a Decision names which window fired
	Start time.Time `json:"start,omitempty" yaml:"start,omitempty"`
	End   time.Time `json:"end,omitempty" yaml:"end,omitempty"`
}

// Policy is loaded straight off a human-authored HISS_POLICY YAML file
// (see loadPolicy in hiss.go) — the tags below ARE that file's schema. It is
// hiss's whole authority: everything Evaluate checks is a lookup into one of
// these fields, never a computed threshold.
type Policy struct {
	Version         string                                       `json:"version,omitempty" yaml:"version,omitempty"`                 // stamped onto every Decision as PolicyVersion — the audit trail's answer to "governed under which rules?"
	Floors          map[string]map[proposal.FailureClass]float64 `json:"floors,omitempty" yaml:"floors,omitempty"`                   // ServiceTier -> FailureClass -> minimum Confidence; below it is ReasonConfidenceFloor
	MaxBand         map[string]decision.Band                     `json:"maxBand,omitempty" yaml:"maxBand,omitempty"`                 // ServiceTier -> ceiling Band; a requested Band ranked higher is ReasonAuthorityCeiling
	AutoBand        map[string]decision.Band                     `json:"autoBand,omitempty" yaml:"autoBand,omitempty"`               // ServiceTier -> ceiling Band for RiskBand, the computed risk; a tier missing from this map ranks its ceiling above every real band, so an unconfigured tier never holds
	FreezeWindows   []Window                                     `json:"freezeWindows,omitempty" yaml:"freezeWindows,omitempty"`     // any Window containing now adds ReasonFreezeWindow
	RequireReversal bool                                         `json:"requireReversal,omitempty" yaml:"requireReversal,omitempty"` // true escalates any Candidate with no ReversalPath as ReasonIrreversible
}
