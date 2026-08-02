package clank

import (
	"errors"
	"time"

	"github.com/ianeff/thump/internal/configfile"
)

// ErrIncompleteLimits means a limits file omitted a term rather than
// setting it.
var ErrIncompleteLimits = errors.New("limits file is missing one or more required terms")

// Limits bounds clank's case base, ledger, change lookback, reason-loop
// depth, and retry budget — sizing and horizon knobs, not scoring math,
// which is why they live apart from ScoringWeights.
type Limits struct {
	// MaxCases bounds CaseBase.MaxCases.
	MaxCases int

	// LedgerRetention bounds MemProposalLog.LedgerRetention.
	LedgerRetention time.Duration

	// ChangeLookback bounds ArgoChangeSource.ChangeLookback.
	ChangeLookback time.Duration

	// MaxProposeAttempts bounds Transport.MaxProposeAttempts.
	MaxProposeAttempts int

	// MaxSteps bounds Engine.MaxSteps.
	MaxSteps int
}

// limitsFile stages a limits.yaml before validation — every field a
// pointer, and the two durations staged as *string, for the same reasons
// weightsFile does (see weights.go's doc comment).
type limitsFile struct {
	MaxCases           *int    `json:"maxCases"`
	LedgerRetention    *string `json:"ledgerRetention"`
	ChangeLookback     *string `json:"changeLookback"`
	MaxProposeAttempts *int    `json:"maxProposeAttempts"`
	MaxSteps           *int    `json:"maxSteps"`
}

// LoadLimitsFile reads path as a YAML file and validates it into a
// Limits — mirrors LoadWeightsFile's posture: fail at load, never at first
// use.
func LoadLimitsFile(path string) (Limits, error) {
	lf, err := configfile.Stage[limitsFile](path, "limits file")
	if err != nil {
		return Limits{}, err
	}

	r := configfile.Require("limits file", ErrIncompleteLimits)
	out := Limits{
		MaxCases:           r.Int("maxCases", lf.MaxCases),
		LedgerRetention:    r.Duration("ledgerRetention", lf.LedgerRetention),
		ChangeLookback:     r.Duration("changeLookback", lf.ChangeLookback),
		MaxProposeAttempts: r.Int("maxProposeAttempts", lf.MaxProposeAttempts),
		MaxSteps:           r.Int("maxSteps", lf.MaxSteps),
	}
	if err := r.Err(); err != nil {
		return Limits{}, err
	}
	return out, nil
}
