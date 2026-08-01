package clank

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// ErrIncompleteWeights means a weights file omitted a term rather
// than setting it.
var ErrIncompleteWeights = errors.New("weights file is missing one or more required terms")

// ScoringWeights tunes CausalScorerImpl.Score — how much temporal recency,
// topological proximity, and historical case-base alignment each contribute
// to a CausalScore.Likelihood. These are scorer tuning, not policy: unlike
// hiss's confidence floors, they shape how the scorer weighs evidence, not
// what the system is allowed to act on, which is why they stay in clank.
type ScoringWeights struct {
	Temporal          float64
	Topological       float64
	Historical        float64
	FreshnessHalfLife time.Duration // how fast historical alignment decays by topology staleness (defence 2)

	// GroundingNone, GroundingOne, and GroundingMany are scoreConfidence's
	// multiplier for a candidate whose citations resolve to 0, 1, or 2+
	// live, in-topology EvidenceRefs — the same tiered floor causal.go
	// already applies to Likelihood, applied here to emitted confidence.
	GroundingNone float64
	GroundingOne  float64
	GroundingMany float64

	// Causal is how much a fully implicated in-topology change may raise a
	// candidate's confidence — 0.5 means up to +50%. It is a bonus rather than
	// a factor because LikelihoodOK drops the term out entirely when no change
	// resolves: as a factor, a run holding corroborating change data scored
	// below the same run holding none.
	Causal float64

	// TemporalHalfLife is how fast a change event's temporalScore decays.
	TemporalHalfLife time.Duration

	// HistoricalAloneCap is the ceiling on an uncorroborated historical
	// score.
	HistoricalAloneCap float64
}

// weightsFile stages a weights.yaml before validation. Every field is a
// pointer so YAML's zero-value collapse (an omitted key and an explicit 0.0
// both unmarshal to 0.0 in a float64) doesn't erase the distinction
// LoadWeightsFile exists to enforce. The two half-life fields stage as
// *string, not *time.Duration: sigs.k8s.io/yaml round-trips through
// encoding/json, and time.Duration is a plain int64 to encoding/json — it
// cannot parse "30m", only a raw nanosecond count. Staging as a string and
// calling time.ParseDuration ourselves is what lets the file read the way a
// human would write it.
type weightsFile struct {
	Temporal           *float64 `json:"temporal"`
	Topological        *float64 `json:"topological"`
	Historical         *float64 `json:"historical"`
	FreshnessHalfLife  *string  `json:"freshnessHalfLife"`
	GroundingNone      *float64 `json:"groundingNone"`
	GroundingOne       *float64 `json:"groundingOne"`
	GroundingMany      *float64 `json:"groundingMany"`
	Causal             *float64 `json:"causal"`
	TemporalHalfLife   *string  `json:"temporalHalfLife"`
	HistoricalAloneCap *float64 `json:"historicalAloneCap"`
}

// LoadWeightsFile reads path as a YAML file and validates it into a
// ScoringWeights — the only decoder for the tuning surface, mirroring
// hiss.LoadPolicy's posture: fail at load, never at first use.
func LoadWeightsFile(path string) (ScoringWeights, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config path, not user input
	if err != nil {
		return ScoringWeights{}, fmt.Errorf("read weights file: %w", err)
	}
	var wf weightsFile
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return ScoringWeights{}, fmt.Errorf("parse weights file: %w", err)
	}

	missing := []string{}
	need := func(name string, present bool) {
		if !present {
			missing = append(missing, name)
		}
	}
	need("temporal", wf.Temporal != nil)
	need("topological", wf.Topological != nil)
	need("historical", wf.Historical != nil)
	need("freshnessHalfLife", wf.FreshnessHalfLife != nil)
	need("groundingNone", wf.GroundingNone != nil)
	need("groundingOne", wf.GroundingOne != nil)
	need("groundingMany", wf.GroundingMany != nil)
	need("causal", wf.Causal != nil)
	need("temporalHalfLife", wf.TemporalHalfLife != nil)
	need("historicalAloneCap", wf.HistoricalAloneCap != nil)

	if len(missing) > 0 {
		return ScoringWeights{}, fmt.Errorf("%w: %s", ErrIncompleteWeights, strings.Join(missing, ", "))
	}

	freshness, err := time.ParseDuration(*wf.FreshnessHalfLife)
	if err != nil {
		return ScoringWeights{}, fmt.Errorf("weights file freshnessHalfLife: %w", err)
	}
	temporalHalfLife, err := time.ParseDuration(*wf.TemporalHalfLife)
	if err != nil {
		return ScoringWeights{}, fmt.Errorf("weights file temporalHalfLife: %w", err)
	}

	return ScoringWeights{
		Temporal:           *wf.Temporal,
		Topological:        *wf.Topological,
		Historical:         *wf.Historical,
		FreshnessHalfLife:  freshness,
		GroundingNone:      *wf.GroundingNone,
		GroundingOne:       *wf.GroundingOne,
		GroundingMany:      *wf.GroundingMany,
		Causal:             *wf.Causal,
		TemporalHalfLife:   temporalHalfLife,
		HistoricalAloneCap: *wf.HistoricalAloneCap,
	}, nil
}
