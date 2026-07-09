package hiss

import (
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

type Window struct {
	Name  string    `json:"name,omitempty" yaml:"name,omitempty"`
	Start time.Time `json:"start,omitempty" yaml:"start,omitempty"`
	End   time.Time `json:"end,omitempty" yaml:"end,omitempty"`
}

// Policy is loaded straight off a human-authored HISS_POLICY YAML file
// (see loadPolicy in hiss.go) — the tags below ARE that file's schema.
type Policy struct {
	Version         string                                       `json:"version,omitempty" yaml:"version,omitempty"`
	Floors          map[string]map[proposal.FailureClass]float64 `json:"floors,omitempty" yaml:"floors,omitempty"`
	MaxBand         map[string]decision.Band                     `json:"maxBand,omitempty" yaml:"maxBand,omitempty"`
	FreezeWindows   []Window                                     `json:"freezeWindows,omitempty" yaml:"freezeWindows,omitempty"`
	RequireReversal bool                                         `json:"requireReversal,omitempty" yaml:"requireReversal,omitempty"`
}
