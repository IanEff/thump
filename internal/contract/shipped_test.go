package contract_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/configtest"
	"github.com/ianeff/thump/internal/contract"
)

// canonicalFailureClasses is the closed FailureClass enum written out once —
// Go can't enumerate a string type's consts by reflection, so this list is
// the only place the set is knowable, and both the catalog's class check and
// the definitions' coverage check read it.
func canonicalFailureClasses() map[proposal.FailureClass]bool {
	return map[proposal.FailureClass]bool{
		proposal.ClassDependencySaturation: true,
		proposal.ClassTrafficShift:         true,
		proposal.ClassResourceExhaustion:   true,
		proposal.ClassRedundancyDegraded:   true,
		proposal.ClassServiceFailure:       true,
		proposal.ClassUnknown:              true,
	}
}

// catalogInvariants are the rules an authored contract must satisfy to be
// reachable end to end: a name thump can bind, a class and tier clank can
// select it under, a blast tier hiss's shaper can band, and a reversal to
// fall back to. A typo in any of these unmarshals silently — an action that
// is present in the file and unreachable in the pipeline.
func catalogInvariants() map[string]func(contract.ActionContract) error {
	classes := canonicalFailureClasses()
	tiers := map[proposal.BlastTier]bool{
		proposal.BlastLow: true, proposal.BlastMed: true, proposal.BlastHigh: true,
	}

	return map[string]func(contract.ActionContract) error{
		"declares a name the actuator can bind": func(c contract.ActionContract) error {
			if c.Name == "" {
				return errors.New("empty name")
			}
			return nil
		},
		"declares at least one failure class from the closed enum": func(c contract.ActionContract) error {
			if len(c.ApplicableFailureClasses) == 0 {
				return errors.New("no applicableFailureClasses — no signal will ever reach it")
			}
			for _, fc := range c.ApplicableFailureClasses {
				if !classes[fc] {
					return fmt.Errorf("applicableFailureClasses has %q, not a FailureClass const", fc)
				}
			}
			return nil
		},
		"declares at least one applicable tier": func(c contract.ActionContract) error {
			if len(c.ApplicableTiers) == 0 {
				return errors.New("no applicableTiers")
			}
			return nil
		},
		"declares a blast tier the shaper can band": func(c contract.ActionContract) error {
			if !tiers[c.BlastTier] {
				return fmt.Errorf("blastTier %q is not low/med/high", c.BlastTier)
			}
			return nil
		},
		"declares a reversal method and fallback": func(c contract.ActionContract) error {
			if c.Reversal.Method == "" || c.Reversal.Fallback == "" {
				return fmt.Errorf("reversal is %+v — an irreversible action hiss can only escalate", c.Reversal)
			}
			return nil
		},
		"forecasts an effectiveness delta alongside its severity query": func(c contract.ActionContract) error {
			sc := c.SuccessCriteria
			if sc.SeverityQuery != "" && sc.SeverityReductionPct == 0 {
				return errors.New("severityQuery with no severityReductionPct — the effectiveness delta has no forecast to score")
			}
			return nil
		},
	}
}

// TestShippedCatalog_RestoreOnSuccessMatchesEachActionsAuthoredIntent pins
// restoreOnSuccess per action so the judgment call stays visible in a diff
// rather than silent in YAML: true only for a temporary tuning knob or hold
// whose authored default is the steady state, false everywhere the win
// itself is the remediation.
func TestShippedCatalog_RestoreOnSuccessMatchesEachActionsAuthoredIntent(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"hold-rebalance":                  true,
		"accelerate-recovery":             true,
		"disable-product-catalog-failure": false,
		"disable-cart-failure":            false,
		"restart-cart-pod":                false,
		"throttle-non-critical-paths":     false,
	}

	contracts := configtest.ShippedCatalog(t).Contracts()
	got := make(map[string]bool, len(contracts))
	for _, c := range contracts {
		got[c.Name] = c.Reversal.RestoreOnSuccess
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("restoreOnSuccess drifted from the authored per-action intent (-want +got)", diff)
	}
}

// TestShippedCatalog_EveryContractIsWellFormed replaces the old
// "YAML matches the Go literal" guard: with no literal left, the file itself
// is the source, so the check is that every authored contract is reachable
// rather than that it matches a second copy.
func TestShippedCatalog_EveryContractIsWellFormed(t *testing.T) {
	t.Parallel()

	contracts := configtest.ShippedCatalog(t).Contracts()
	if len(contracts) == 0 {
		t.Fatal("config/actions/catalog.yaml loaded zero contracts — clank can propose nothing")
	}

	for _, c := range contracts {
		for claim, check := range catalogInvariants() {
			t.Run(c.Name+" "+claim, func(t *testing.T) {
				if err := check(c); err != nil {
					t.Errorf("config/actions/catalog.yaml: %v", err)
				}
			})
		}
	}
}
