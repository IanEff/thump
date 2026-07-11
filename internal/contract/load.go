package contract

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/proposal"
)

// PreconditionRegistry maps an authored Precondition's 'Name' to the
// compiled func that evaluates it.
type PreconditionRegistry map[string]func(proposal.SAO) bool

// Preconditions is the production registry.  Load will not bind a
// name this map doesn't have.
var Preconditions = PreconditionRegistry{}

// Load parses a raw YAML document holding an []ActionContract into a
// StaticCatalog, binding every Precondition.OK against reg by name.
func Load(raw []byte, reg PreconditionRegistry) (*StaticCatalog, error) {
	var contracts []ActionContract
	if err := yaml.Unmarshal(raw, &contracts); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}

	for i, c := range contracts {
		for j, p := range c.Preconditions {
			ok, found := reg[p.Name]
			if !found {
				return nil, fmt.Errorf("catalog: contract %q names precondition %q, not in the registry", c.Name, p.Name)
			}
			contracts[i].Preconditions[j].OK = ok
		}
	}
	return NewStaticCatalog(contracts), nil
}

// LoadCatalogFile reads path and parses it with Load.
func LoadCatalogFile(path string, reg PreconditionRegistry) (*StaticCatalog, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read catalog file %s: %w", path, err)
	}
	return Load(raw, reg)
}
