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
			Description: "an upstream/downstream dependency is overloaded for every caller — the resource itself " +
				"is unhealthy, not this one service's connection to it; cite request-rate/failure-rate or upstream " +
				"latency evidence, not just elevated latency on the resource. A single caller failing to reach an " +
				"otherwise-healthy dependency because of its OWN injected fault or bad config is service_failure, not this.",
		},
		{
			Class: proposal.ClassResourceExhaustion,
			Description: "the resource ITSELF is out of headroom — cite capacity evidence (e.g. utilization/fullness). " +
				"Ongoing recovery/backfill activity with healthy capacity is NOT resource_exhaustion.",
		},
		{
			Class: proposal.ClassRedundancyDegraded,
			Description: "the cluster is running below its configured replication — placement groups are degraded or " +
				"undersized, so stored data is at elevated loss risk until recovery restores the full replica count. " +
				"Cite degraded/undersized PG evidence, not capacity fullness (resource_exhaustion) or request/failure " +
				"rate (dependency_saturation).",
		},
		{
			Class:       proposal.ClassTrafficShift,
			Description: "a legitimate change in load pattern, not a failure.",
		},
		{
			Class: proposal.ClassServiceFailure,
			Description: "a service is returning errors on its own request or RPC path, or failing to reach a " +
				"dependency it needs, because of an injected fault or bad config IN THAT SERVICE — not because the " +
				"dependency is degraded for every caller (dependency_saturation) or a resource is out of headroom " +
				"(resource_exhaustion). Cite the service's own error-rate evidence; the fix is disabling the fault, " +
				"not scaling or waiting.",
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
