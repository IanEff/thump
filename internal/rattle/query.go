package rattle

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// ErrIncompleteQueryConfig means a query config file omitted a term rather
// than setting it.
var ErrIncompleteQueryConfig = errors.New("query config file is missing one or more required terms")

// QueryConfig bounds PromSource's query shape — how far apart its samples
// are and how far back it looks — cluster-shaped by the target
// Prometheus's scrape interval, not scorer tuning.
type QueryConfig struct {
	// Step is PromSource.Step — sample spacing.
	Step time.Duration

	// Window is PromSource.Window — how far back to query.
	Window time.Duration
}

// queryConfigFile stages a query.yaml before validation — both fields
// pointers so YAML's zero-value collapse doesn't hide an omitted key, both
// staged as *string for the same reason weightsFile's half-life fields are
// (see weights.go's doc comment): sigs.k8s.io/yaml round-trips through
// encoding/json, which can't parse "15m" into a time.Duration directly.
type queryConfigFile struct {
	Step   *string `json:"step"`
	Window *string `json:"window"`
}

// LoadQueryConfig reads path as a YAML file and validates it into a
// QueryConfig — mirrors clank.LoadWeightsFile's posture: fail at load,
// never at first use.
func LoadQueryConfig(path string) (QueryConfig, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config path, not user input
	if err != nil {
		return QueryConfig{}, fmt.Errorf("read query config file: %w", err)
	}
	var qf queryConfigFile
	if err := yaml.Unmarshal(raw, &qf); err != nil {
		return QueryConfig{}, fmt.Errorf("parse query config file: %w", err)
	}

	missing := []string{}
	need := func(name string, present bool) {
		if !present {
			missing = append(missing, name)
		}
	}
	need("step", qf.Step != nil)
	need("window", qf.Window != nil)

	if len(missing) > 0 {
		return QueryConfig{}, fmt.Errorf("%w: %s", ErrIncompleteQueryConfig, strings.Join(missing, ", "))
	}

	step, err := time.ParseDuration(*qf.Step)
	if err != nil {
		return QueryConfig{}, fmt.Errorf("query config file step: %w", err)
	}
	window, err := time.ParseDuration(*qf.Window)
	if err != nil {
		return QueryConfig{}, fmt.Errorf("query config file window: %w", err)
	}

	return QueryConfig{Step: step, Window: window}, nil
}
