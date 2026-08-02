package clank

import (
	"errors"
	"time"

	"github.com/ianeff/thump/internal/configfile"
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
	Temporal           float64
	Topological        float64
	Historical         float64
	HistoricalHalfLife time.Duration // how fast historical alignment decays by topology staleness (defence 2)

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

	// RecencyHalfLife is how fast a change event's temporalScore decays.
	RecencyHalfLife time.Duration

	// HistoricalAloneCap is the ceiling on an uncorroborated historical
	// score.
	HistoricalAloneCap float64

	// CaseBaseBaseline is the historical score assigned before freshness
	// decay when no case-base prior corroborates it yet.
	CaseBaseBaseline float64

	// NegativeSignalPenalty is how much likelihood a predicted-but-absent
	// signal costs — defence 3.
	NegativeSignalPenalty float64
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
	Temporal              *float64 `json:"temporal"`
	Topological           *float64 `json:"topological"`
	Historical            *float64 `json:"historical"`
	HistoricalHalfLife    *string  `json:"historicalHalfLife"`
	GroundingNone         *float64 `json:"groundingNone"`
	GroundingOne          *float64 `json:"groundingOne"`
	GroundingMany         *float64 `json:"groundingMany"`
	Causal                *float64 `json:"causal"`
	RecencyHalfLife       *string  `json:"recencyHalfLife"`
	HistoricalAloneCap    *float64 `json:"historicalAloneCap"`
	CaseBaseBaseline      *float64 `json:"caseBaseBaseline"`
	NegativeSignalPenalty *float64 `json:"negativeSignalPenalty"`
}

// LoadWeightsFile reads path as a YAML file and validates it into a
// ScoringWeights — the only decoder for the tuning surface, mirroring
// hiss.LoadPolicy's posture: fail at load, never at first use.
func LoadWeightsFile(path string) (ScoringWeights, error) {
	wf, err := configfile.Stage[weightsFile](path, "weights file")
	if err != nil {
		return ScoringWeights{}, err
	}

	r := configfile.Require("weights file", ErrIncompleteWeights)
	out := ScoringWeights{
		Temporal:              r.Float("temporal", wf.Temporal),
		Topological:           r.Float("topological", wf.Topological),
		Historical:            r.Float("historical", wf.Historical),
		HistoricalHalfLife:    r.Duration("historicalHalfLife", wf.HistoricalHalfLife),
		GroundingNone:         r.Float("groundingNone", wf.GroundingNone),
		GroundingOne:          r.Float("groundingOne", wf.GroundingOne),
		GroundingMany:         r.Float("groundingMany", wf.GroundingMany),
		Causal:                r.Float("causal", wf.Causal),
		RecencyHalfLife:       r.Duration("recencyHalfLife", wf.RecencyHalfLife),
		HistoricalAloneCap:    r.Float("historicalAloneCap", wf.HistoricalAloneCap),
		CaseBaseBaseline:      r.Float("caseBaseBaseline", wf.CaseBaseBaseline),
		NegativeSignalPenalty: r.Float("negativeSignalPenalty", wf.NegativeSignalPenalty),
	}
	if err := r.Err(); err != nil {
		return ScoringWeights{}, err
	}
	return out, nil
}
