package rattle

import (
	"errors"
	"time"

	"github.com/ianeff/thump/internal/configfile"
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
	qf, err := configfile.Stage[queryConfigFile](path, "query config file")
	if err != nil {
		return QueryConfig{}, err
	}

	r := configfile.Require("query config file", ErrIncompleteQueryConfig)
	out := QueryConfig{
		Step:   r.Duration("step", qf.Step),
		Window: r.Duration("window", qf.Window),
	}
	if err := r.Err(); err != nil {
		return QueryConfig{}, err
	}
	return out, nil
}
