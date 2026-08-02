package clank

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
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
	raw, err := os.ReadFile(path) //nolint:gosec // G304: operator-supplied config path, not user input
	if err != nil {
		return Limits{}, fmt.Errorf("read limits file: %w", err)
	}
	var lf limitsFile
	if err := yaml.Unmarshal(raw, &lf); err != nil {
		return Limits{}, fmt.Errorf("parse limits file: %w", err)
	}

	missing := []string{}
	need := func(name string, present bool) {
		if !present {
			missing = append(missing, name)
		}
	}
	need("maxCases", lf.MaxCases != nil)
	need("ledgerRetention", lf.LedgerRetention != nil)
	need("changeLookback", lf.ChangeLookback != nil)
	need("maxProposeAttempts", lf.MaxProposeAttempts != nil)
	need("maxSteps", lf.MaxSteps != nil)

	if len(missing) > 0 {
		return Limits{}, fmt.Errorf("%w: %s", ErrIncompleteLimits, strings.Join(missing, ", "))
	}

	ledgerRetention, err := time.ParseDuration(*lf.LedgerRetention)
	if err != nil {
		return Limits{}, fmt.Errorf("limits file ledgerRetention: %w", err)
	}
	changeLookback, err := time.ParseDuration(*lf.ChangeLookback)
	if err != nil {
		return Limits{}, fmt.Errorf("limits file changeLookback: %w", err)
	}

	return Limits{
		MaxCases:           *lf.MaxCases,
		LedgerRetention:    ledgerRetention,
		ChangeLookback:     changeLookback,
		MaxProposeAttempts: *lf.MaxProposeAttempts,
		MaxSteps:           *lf.MaxSteps,
	}, nil
}
