package rattle

import (
	"errors"
	"time"

	"github.com/ianeff/thump/internal/configfile"
)

// ErrIncompleteQueryConfig means a query config file omitted a term rather
// than setting it.
var ErrIncompleteQueryConfig = errors.New("query config file is missing one or more required terms")

// QueryConfig bounds PromSource's query shape and the reconcile loop's own
// cadence — cluster-shaped by the target Prometheus's scrape interval and
// traffic density, not scorer tuning.
type QueryConfig struct {
	// Step is PromSource.Step — sample spacing.
	Step time.Duration

	// Window is PromSource.Window — how far back to query.
	Window time.Duration

	// PollInterval is runLoop's reconcile cadence.
	PollInterval time.Duration

	// ReconcileTimeout bounds one reconcile tick — must stay under
	// PollInterval or ticks overlap and the beat queues nothing but dead
	// work.
	ReconcileTimeout time.Duration

	// SustainedMinSamples is SustainedBurnDetector.MinSamples — how many
	// consecutive above-threshold samples a burn needs before it fires.
	SustainedMinSamples int

	// Debounce is how long NewDebouncer holds a fingerprint down after it
	// fires, before the same SLO can fire again.
	Debounce time.Duration

	// FreshnessBound is SignalContract.FreshnessBound — a sample older than
	// this reads as a broken scrape path, not a healthy service.
	FreshnessBound time.Duration
}

// queryConfigFile stages a query.yaml before validation — every field a
// pointer so YAML's zero-value collapse doesn't hide an omitted key, the
// duration fields staged as *string for the same reason weightsFile's
// half-life fields are (see weights.go's doc comment): sigs.k8s.io/yaml
// round-trips through encoding/json, which can't parse "15m" into a
// time.Duration directly.
type queryConfigFile struct {
	Step                *string `json:"step"`
	Window              *string `json:"window"`
	PollInterval        *string `json:"pollInterval"`
	ReconcileTimeout    *string `json:"reconcileTimeout"`
	SustainedMinSamples *int    `json:"sustainedMinSamples"`
	Debounce            *string `json:"debounce"`
	FreshnessBound      *string `json:"freshnessBound"`
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
		Step:                r.Duration("step", qf.Step),
		Window:              r.Duration("window", qf.Window),
		PollInterval:        r.Duration("pollInterval", qf.PollInterval),
		ReconcileTimeout:    r.Duration("reconcileTimeout", qf.ReconcileTimeout),
		SustainedMinSamples: r.Int("sustainedMinSamples", qf.SustainedMinSamples),
		Debounce:            r.Duration("debounce", qf.Debounce),
		FreshnessBound:      r.Duration("freshnessBound", qf.FreshnessBound),
	}
	if err := r.Err(); err != nil {
		return QueryConfig{}, err
	}
	return out, nil
}
