// Package contract is the boundary vocabulary of the authored action
// catalog: the ActionContract and its scaffolding, shared by the beat that
// proposes from it (clank) and the beat that executes from it (thump). It
// is a leaf — time and internal/proposal only, an invariant pinned by
// leaf_test.go — so no beat imports another beat's internals to reach the
// catalog.
package contract

import (
	"errors"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
)

// ErrOutsideCatalog is the autonomy boundary's refusal: a ContractRef that
// names no authored contract can neither be proposed (clank) nor executed
// (thump).
var ErrOutsideCatalog = errors.New("contract: proposed contract not in catalog")

type StaticCatalog struct {
	contracts []ActionContract
}

func NewStaticCatalog(t []ActionContract) *StaticCatalog {
	return &StaticCatalog{contracts: t}
}

func (s *StaticCatalog) Applicable(class proposal.FailureClass, tier string, sao proposal.SAO) []ActionContract {
	var applicableContracts []ActionContract
	for _, c := range s.contracts {
		if !classMatches(c, class) {
			continue
		}
		if !tierMatches(c, tier) {
			continue
		}
		if !preconditionsMet(c, sao) {
			continue
		}
		applicableContracts = append(applicableContracts, c)
	}
	return applicableContracts
}

// ApplicableToTier lists the contracts the signal's tier and the SAO's
// preconditions admit, across all failure classes — the menu the model may
// propose from before it has committed to a FailureClass. The class filter is
// applied afterward by the engine's enforceCatalog backstop, once the model has
// chosen one. Without this menu in the prompt, a real model invents plausible
// contractRefs that aren't in the catalog.
func (s *StaticCatalog) ApplicableToTier(tier string, sao proposal.SAO) []ActionContract {
	var out []ActionContract
	for _, c := range s.contracts {
		if !tierMatches(c, tier) {
			continue
		}
		if !preconditionsMet(c, sao) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ByName resolves a granted ContractRef to its authored contract. The
// boolean is the I-4 boundary: false means "not in the catalog", and
// nothing downstream of a false ever executes.
func (s *StaticCatalog) ByName(name string) (ActionContract, bool) {
	for _, c := range s.contracts {
		if c.Name == name {
			return c, true
		}
	}
	return ActionContract{}, false
}

// Contracts returns every authored contract in load order — the read side
// LoadCatalogFile's golden tests (and any future catalog inspector) use to
// look inside a StaticCatalog, since the contracts slice itself stays
// unexported.
func (s *StaticCatalog) Contracts() []ActionContract {
	return s.contracts
}

func classMatches(c ActionContract, class proposal.FailureClass) bool {
	for _, fc := range c.ApplicableFailureClasses {
		if fc == class {
			return true
		}
	}
	return false
}

func tierMatches(c ActionContract, tier string) bool {
	for _, t := range c.ApplicableTiers {
		if t == tier {
			return true
		}
	}
	return false
}

func preconditionsMet(c ActionContract, sao proposal.SAO) bool {
	for _, p := range c.Preconditions {
		if !p.OK(sao) {
			return false
		}
	}
	return true
}

type ActionContract struct {
	Name                     string                  `json:"name,omitempty" yaml:"name,omitempty"`
	ApplicableFailureClasses []proposal.FailureClass `json:"applicableFailureClasses,omitempty" yaml:"applicableFailureClasses,omitempty"`
	ApplicableTiers          []string                `json:"applicableTiers,omitempty" yaml:"applicableTiers,omitempty"`
	Preconditions            []Precondition          `json:"preconditions,omitempty" yaml:"preconditions,omitempty"`
	Action                   ActionSpec              `json:"action,omitempty" yaml:"action,omitempty"`
	Reversal                 Reversal                `json:"reversal,omitempty" yaml:"reversal,omitempty"`
	SuccessCriteria          SuccessCriteria         `json:"successCriteria,omitempty" yaml:"successCriteria,omitempty"`
}

type ActionSpec struct {
	Description     string           `json:"description,omitempty" yaml:"description,omitempty"`
	ScopeParameters map[string]Range `json:"scopeParameters,omitempty" yaml:"scopeParameters,omitempty"`
}

type Range struct {
	Min     float64 `json:"min,omitempty" yaml:"min,omitempty"`
	Max     float64 `json:"max,omitempty" yaml:"max,omitempty"`
	Default float64 `json:"default,omitempty" yaml:"default,omitempty"`
}

type Reversal struct {
	Method   string `json:"method,omitempty" yaml:"method,omitempty"`
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"`
}

type SuccessCriteria struct {
	Metric          string        `json:"metric,omitempty" yaml:"metric,omitempty"`
	Target          string        `json:"target,omitempty" yaml:"target,omitempty"`
	Window          time.Duration `json:"window,omitempty" yaml:"window,omitempty"`
	AbortConditions []string      `json:"abortConditions,omitempty" yaml:"abortConditions,omitempty"`
}

type Precondition struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// OK is unmarshalable by construction — a precondition is authored Go,
	// never data on the wire.
	OK func(proposal.SAO) bool `json:"-" yaml:"-"`
}
