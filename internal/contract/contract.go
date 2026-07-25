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
	BlastTier                proposal.BlastTier      `json:"blastTier,omitempty" yaml:"blastTier,omitempty"`
	Preconditions            []Precondition          `json:"preconditions,omitempty" yaml:"preconditions,omitempty"`
	Action                   ActionSpec              `json:"action,omitempty" yaml:"action,omitempty"`
	Reversal                 Reversal                `json:"reversal,omitempty" yaml:"reversal,omitempty"`
	Execution                Execution               `json:"execution,omitempty" yaml:"execution,omitempty"`
	SuccessCriteria          SuccessCriteria         `json:"successCriteria,omitempty" yaml:"successCriteria,omitempty"`
}

type ActionSpec struct {
	Description     string           `json:"description,omitempty" yaml:"description,omitempty"`
	ScopeParameters map[string]Range `json:"scopeParameters,omitempty" yaml:"scopeParameters,omitempty"`
}

// Execution is the mechanism half of an authored action — the steps that
// actually mutate the cluster, as against Reversal's audit label for the same
// undo. Neither list may be empty: internal/actuate refuses to bind a contract
// missing either half, so an irreversible action cannot be authored at all.
type Execution struct {
	Forward []Step `json:"forward,omitempty" yaml:"forward,omitempty"` // the mutation the action performs — empty is ErrUnbindable at load, so a catalogued action is never a no-op
	Reverse []Step `json:"reverse,omitempty" yaml:"reverse,omitempty"` // the mutation that undoes it, authored beside it so the two can't drift apart
}

// Step names one bounded mechanism and its target.  Verb selects from
// a closed set compiled into the actuator.
type Step struct {
	Verb       string   `json:"verb,omitempty" yaml:"verb,omitempty"`
	Namespace  string   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Selector   string   `json:"selector,omitempty" yaml:"selector,omitempty"`
	Command    []string `json:"command,omitempty" yaml:"command,omitempty"`
	Deployment string   `json:"deployment,omitempty" yaml:"deployment,omitempty"`
	// Replicas is a pointer so a scale-to-zero stays distinguishable from
	// an omitted count — a plain int decodes a missing key as a silent 0.
	Replicas  *int   `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	ConfigMap string `json:"configMap,omitempty" yaml:"configMap,omitempty"`
	DataKey   string `json:"dataKey,omitempty" yaml:"dataKey,omitempty"`
	Flag      string `json:"flag,omitempty" yaml:"flag,omitempty"`
	Variant   string `json:"variant,omitempty" yaml:"variant,omitempty"`
}

type Range struct {
	Min     float64 `json:"min,omitempty" yaml:"min,omitempty"`
	Max     float64 `json:"max,omitempty" yaml:"max,omitempty"`
	Default float64 `json:"default,omitempty" yaml:"default,omitempty"`
}

// Reversal is the label half of an authored undo — what the audit trail and
// an operator call it, never how it runs. The mutation itself is
// Execution.Reverse; nothing checks that the two describe each other, so a
// Method naming something Execution.Reverse doesn't do is a config error no
// loader will catch.
type Reversal struct {
	Method   string `json:"method,omitempty" yaml:"method,omitempty"`     // operator-facing name for the undo; reaches the audit trail as Order.Reversal.Method and the Candidate's ReversalPath
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"` // what to do when the undo itself fails — hiss escalates rather than retrying
}

type SuccessCriteria struct {
	Metric          string        `json:"metric,omitempty" yaml:"metric,omitempty"`
	Target          string        `json:"target,omitempty" yaml:"target,omitempty"`
	Window          time.Duration `json:"window,omitempty" yaml:"window,omitempty"`
	AbortConditions []string      `json:"abortConditions,omitempty" yaml:"abortConditions,omitempty"`
	SeverityQuery   string        `json:"severityQuery,omitempty" yaml:"severityQuery,omitempty"` // Normalized fraction of the SLO's error budget as returned by PromQL
	// SeverityReductionPct is the authored expectation of how much this
	// action cuts the SeverityQuery's 0..1 error-budget severity — the
	// predicted end of the effectiveness delta whose observed end is the
	// convergence Outcome's ObservedSeverity. Only meaningful alongside a
	// SeverityQuery; a zero value means unforecast and feeds no effectiveness
	// datum, never an expectation of zero effect.
	SeverityReductionPct float64 `json:"severityReductionPct,omitempty" yaml:"severityReductionPct,omitempty"`
}

type Precondition struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// OK is unmarshalable by construction — a precondition is authored Go,
	// never data on the wire.
	OK func(proposal.SAO) bool `json:"-" yaml:"-"`
}
