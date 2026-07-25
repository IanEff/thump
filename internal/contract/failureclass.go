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
