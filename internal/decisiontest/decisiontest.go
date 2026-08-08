// Package decisiontest builds decision.Governed fixtures shared across
// beats' tests — the one shape a broker-mode read is asked to find, so
// internal/incident and internal/calipers can't drift on what "one governed
// decision parked on a human" means.
package decisiontest

import (
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
)

// Held returns one governed decision parked on a human at fingerprint — the
// shape SnapshotBroker and break-glass force both exist to surface.
func Held(fingerprint string) decision.Governed {
	return decision.Governed{
		Decision: decision.Decision{
			ID: "dec:" + fingerprint, SignalRef: fingerprint,
			Verdict:       decision.VerdictHold,
			RequestedBand: decision.BandActDisruptive,
			RiskBand:      decision.BandActDisruptive,
			PolicyVersion: "policy-v3",
			EvaluatedAt:   time.Now(),
			Reasons:       []string{decision.ReasonRiskCeiling},
		},
		Set: proposal.Set{SignalRef: fingerprint},
	}
}
