package contract

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/proposal"
)

// FailureClassDefinition is the authored, plain-English meaning of one
// FailureClass — rig-invariant knowledge every site shares, unlike the
// evidence-tool query names that back it (those vary by site; see
// evidence-queries.yaml). clank's seedPrompt renders this list so the model
// is told what a class means instead of inferring it from which action
// happens to be catalogued for it.
type FailureClassDefinition struct {
	Class       proposal.FailureClass `json:"class,omitempty" yaml:"class,omitempty"`
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
}

// DefaultFailureClasses is the compiled-in authored default — the single
// source config/actions/failure-classes.yaml is pinned against
// (TestShippedFailureClassesMatchesAuthoredDefault), same discipline as
// Default() and catalog.yaml.
func DefaultFailureClasses() []FailureClassDefinition {
	return []FailureClassDefinition{
		{
			Class: proposal.ClassDependencySaturation,
			Description: "an upstream/downstream dependency is overloaded — the resource itself is fine; " +
				"cite request-rate/failure-rate or upstream latency evidence, not just elevated latency on the resource.",
		},
		{
			Class: proposal.ClassResourceExhaustion,
			Description: "the resource ITSELF is out of headroom — cite capacity evidence (e.g. utilization/fullness). " +
				"Ongoing recovery/backfill activity with healthy capacity is NOT resource_exhaustion.",
		},
		{
			Class:       proposal.ClassTrafficShift,
			Description: "a legitimate change in load pattern, not a failure.",
		},
		{
			Class: proposal.ClassUnknown,
			Description: "the evidence doesn't clearly support any of the above — call insufficient rather than " +
				"forcing a label just because an action exists for it.",
		},
	}
}

// LoadFailureClasses parses a raw YAML document holding
// []FailureClassDefinition.
func LoadFailureClasses(raw []byte) ([]FailureClassDefinition, error) {
	var defs []FailureClassDefinition
	if err := yaml.Unmarshal(raw, &defs); err != nil {
		return nil, fmt.Errorf("parse failure classes: %w", err)
	}
	return defs, nil
}

// LoadFailureClassesFile reads path and parses it with LoadFailureClasses.
func LoadFailureClassesFile(path string) ([]FailureClassDefinition, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read failure classes file %s: %w", path, err)
	}
	return LoadFailureClasses(raw)
}
