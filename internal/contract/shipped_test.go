package contract_test

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"testing"
	"time"

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
		"acme-shed-load":                  false,
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

// TestShippedCatalog_HoldOnMissMatchesEachActionsAuthoredIntent pins
// holdOnMiss per action the same way its RestoreOnSuccess sibling does: true
// only where the forward action is itself the remediation, so a missed
// window's undo would re-inject the fault it just cleared.
func TestShippedCatalog_HoldOnMissMatchesEachActionsAuthoredIntent(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"hold-rebalance":                  false,
		"accelerate-recovery":             false,
		"disable-product-catalog-failure": true,
		"disable-cart-failure":            true,
		"restart-cart-pod":                false,
		"throttle-non-critical-paths":     false,
		"acme-shed-load":                  false,
	}

	contracts := configtest.ShippedCatalog(t).Contracts()
	got := make(map[string]bool, len(contracts))
	for _, c := range contracts {
		got[c.Name] = c.Reversal.HoldOnMiss
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("holdOnMiss drifted from the authored per-action intent (-want +got)", diff)
	}
}

// TestShippedCatalog_HoldOnMissNeverDowngradesReversibility guards the
// distinction the flag exists to preserve: holdOnMiss changes when the undo
// fires, never whether it exists — RiskBand's act_reversible grant reads
// Method being authored, not the trigger policy around it.
func TestShippedCatalog_HoldOnMissNeverDowngradesReversibility(t *testing.T) {
	t.Parallel()

	contracts := configtest.ShippedCatalog(t).Contracts()
	for _, c := range contracts {
		if c.Reversal.HoldOnMiss && c.Reversal.Method == "" {
			t.Errorf("%s: holdOnMiss is a trigger policy, not a reversibility downgrade — it must still declare reversal.method", c.Name)
		}
	}
}

// rangeSelector pulls a PromQL range-vector selector's duration, e.g. the
// "5m" out of "rate(foo[5m])" — the window a rate() call smooths over,
// and so the shortest interval a success-window comparison can trust.
var rangeSelector = regexp.MustCompile(`\[(\d+)([smh])\]`)

// maxRangeSelector reports the longest range-vector window named anywhere in
// query, zero when the query has none (an instant query like ceph_health has
// no rate() to smooth, so nothing constrains its success window from below).
func maxRangeSelector(t *testing.T, query string) time.Duration {
	t.Helper()
	var longest time.Duration
	for _, m := range rangeSelector.FindAllStringSubmatch(query, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("range selector %q: %v", m[0], err)
		}
		var unit time.Duration
		switch m[2] {
		case "s":
			unit = time.Second
		case "m":
			unit = time.Minute
		case "h":
			unit = time.Hour
		}
		longest = max(longest, time.Duration(n)*unit)
	}
	return longest
}

// TestShippedCatalog_SuccessWindowOutlivesItsMetricsRateWindow pins the
// config relationship no loader checks: a success window no longer than the
// rate() range its own metric is smoothed over can never observe a clean
// read, so a genuine recovery still reads as a miss. AP's Run 1 hit this
// live — window == rate-window with an exact-==0 target, on a metric that
// only ever decays toward zero without ever landing on it.
func TestShippedCatalog_SuccessWindowOutlivesItsMetricsRateWindow(t *testing.T) {
	t.Parallel()

	queries := configtest.EvidenceQueriesForProfile(t, "thump-test").Queries
	contracts := configtest.ShippedCatalog(t).Contracts()
	for _, c := range contracts {
		t.Run(c.Name, func(t *testing.T) {
			q, ok := queries[c.SuccessCriteria.Metric]
			if !ok {
				t.Fatalf("no evidence query named %q for successCriteria.metric", c.SuccessCriteria.Metric)
			}
			maxRange := maxRangeSelector(t, q.Query)
			if c.SuccessCriteria.Window <= maxRange {
				t.Errorf("successCriteria.window %s must be strictly longer than %q's own rate window %s",
					c.SuccessCriteria.Window, c.SuccessCriteria.Metric, maxRange)
			}
		})
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
