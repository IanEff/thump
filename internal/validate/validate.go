// Package validate asserts that an authored profile directory satisfies all
// catalog invariants, governance policy coverage rules, and telemetry schemas —
// catching unreachable actions and silent configuration drift before execution.
package validate

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
	"github.com/ianeff/thump/internal/evidence"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/rattle"
)

// ErrValidationFailed indicates that one or more authored configuration files
// violated invariants or schema constraints.
var ErrValidationFailed = errors.New("validation failed")

// Canonical failure classes permitted in catalog and policy authoring.
var canonicalFailureClasses = map[proposal.FailureClass]bool{
	proposal.ClassDependencySaturation: true,
	proposal.ClassTrafficShift:         true,
	proposal.ClassResourceExhaustion:   true,
	proposal.ClassRedundancyDegraded:   true,
	proposal.ClassServiceFailure:       true,
	proposal.ClassUnknown:              true,
}

var canonicalBlastTiers = map[proposal.BlastTier]bool{
	proposal.BlastLow:  true,
	proposal.BlastMed:  true,
	proposal.BlastHigh: true,
}

var canonicalVerbs = map[string]bool{
	"scale":       true,
	"restart":     true,
	"flagVariant": true,
	"exec":        true,
}

var canonicalBands = map[decision.Band]bool{
	decision.BandObserve:       true,
	decision.BandActReversible: true,
	decision.BandActDisruptive: true,
}

// ProfileResult captures the counts and findings of validating a single profile.
type ProfileResult struct {
	Profile         string  // profile name or path
	Actions         int     // number of action contracts validated
	FailureClasses  int     // number of failure class descriptions validated
	PolicyFloors    int     // number of (tier, class) policy floors validated
	WatchSLOs       int     // number of SLOs validated
	EvidenceQueries int     // number of evidence queries validated
	Errors          []error // all invariant failures discovered
}

// Profile audits an authored profile directory (e.g. config/dev or
// test/onboarding/testdata/acme), verifying that all contracts, governance
// floors, evidence queries, and SLOs agree without contradiction or missing links.
func Profile(dir string) (ProfileResult, error) {
	res := ProfileResult{
		Profile: filepath.Base(dir),
	}

	catalogFile := findFile(dir, "actions/catalog.yaml", "catalog.yaml")
	fcFile := findFile(dir, "actions/failure-classes.yaml", "failure-classes.yaml")
	policyFile := findFile(dir, "hiss/policy.yaml", "policy.yaml")
	watchFile := findFile(dir, "rattle/watch.yaml", "watch.yaml")
	evidenceFile := findFile(dir, "whir/evidence-queries.yaml", "evidence-queries.yaml")

	var cat *contract.StaticCatalog
	if catalogFile != "" {
		loadedCat, err := contract.LoadCatalogFile(catalogFile, contract.Preconditions)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("load catalog %s: %w", catalogFile, err))
		} else {
			cat = loadedCat
			actions := cat.Contracts()
			res.Actions = len(actions)
			for _, act := range actions {
				if errs := validateActionContract(act); len(errs) > 0 {
					for _, e := range errs {
						res.Errors = append(res.Errors, fmt.Errorf("catalog %s action %q: %w", catalogFile, act.Name, e))
					}
				}
			}
		}
	} else {
		res.Errors = append(res.Errors, fmt.Errorf("no catalog.yaml found in %s", dir))
	}

	if fcFile != "" {
		classes, err := contract.LoadFailureClassesFile(fcFile)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("load failure classes %s: %w", fcFile, err))
		} else {
			res.FailureClasses = len(classes)
			definedClasses := make(map[proposal.FailureClass]bool)
			for _, fc := range classes {
				if strings.TrimSpace(fc.Description) == "" {
					res.Errors = append(res.Errors, fmt.Errorf("failure-classes %s: class %q has empty description", fcFile, fc.Class))
				}
				definedClasses[fc.Class] = true
			}
			if cat != nil {
				for _, act := range cat.Contracts() {
					for _, c := range act.ApplicableFailureClasses {
						if !definedClasses[c] {
							res.Errors = append(res.Errors, fmt.Errorf("action %q uses failure class %q not defined in %s", act.Name, c, fcFile))
						}
					}
				}
			}
		}
	}

	if policyFile != "" {
		pol, err := hiss.LoadPolicy(policyFile)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("load policy %s: %w", policyFile, err))
		} else {
			if pol.Version == "" {
				res.Errors = append(res.Errors, fmt.Errorf("policy %s: missing version string", policyFile))
			}
			floorCount := 0
			for _, fcMap := range pol.Floors {
				floorCount += len(fcMap)
			}
			res.PolicyFloors = floorCount

			if cat != nil {
				for _, act := range cat.Contracts() {
					for _, tier := range act.ApplicableTiers {
						if len(pol.MaxBand) > 0 {
							if mb, ok := pol.MaxBand[tier]; !ok || !canonicalBands[mb] {
								res.Errors = append(res.Errors, fmt.Errorf("policy %s: tier %q used in action %q missing from maxBand", policyFile, tier, act.Name))
							}
						}
						if len(pol.AutoBand) > 0 {
							if ab, ok := pol.AutoBand[tier]; !ok || !canonicalBands[ab] {
								res.Errors = append(res.Errors, fmt.Errorf("policy %s: tier %q used in action %q missing from autoBand", policyFile, tier, act.Name))
							}
						}
						for _, fc := range act.ApplicableFailureClasses {
							tierFloors, ok := pol.Floors[tier]
							if !ok {
								res.Errors = append(res.Errors, fmt.Errorf("policy %s: missing confidence floors for tier %q (action %q)", policyFile, tier, act.Name))
								continue
							}
							floor, ok := tierFloors[fc]
							if !ok || floor <= 0 {
								res.Errors = append(res.Errors, fmt.Errorf("policy %s: missing confidence floor for actuatable pair (%s, %s) in action %q", policyFile, tier, fc, act.Name))
							} else if floor > 1.0 {
								res.Errors = append(res.Errors, fmt.Errorf("policy %s: confidence floor %.2f for (%s, %s) exceeds 1.0", policyFile, floor, tier, fc))
							}
						}
					}
				}
			}
		}
	} else {
		res.Errors = append(res.Errors, fmt.Errorf("no policy.yaml found in %s", dir))
	}

	if evidenceFile != "" {
		cfg, err := evidence.LoadEvidenceConfig(evidenceFile)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("load evidence queries %s: %w", evidenceFile, err))
		} else {
			res.EvidenceQueries = len(cfg.Queries)
			for _, q := range cfg.Queries {
				if q.Name == "" || q.Query == "" {
					res.Errors = append(res.Errors, fmt.Errorf("evidence-queries %s: query missing name or PromQL query string", evidenceFile))
				}
			}
		}
	}

	if watchFile != "" {
		slos, err := rattle.LoadWatch(watchFile)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("load watch %s: %w", watchFile, err))
		} else {
			res.WatchSLOs = len(slos)
			if len(slos) == 0 {
				res.Errors = append(res.Errors, fmt.Errorf("watch %s declares zero SLOs", watchFile))
			}
			for _, slo := range slos {
				if slo.ID == "" || slo.Object == "" || slo.Tier == "" || slo.ContractRef == "" {
					res.Errors = append(res.Errors, fmt.Errorf("watch %s: SLO %+v has missing required fields", watchFile, slo))
				}
				if slo.Objective <= 0 || slo.Objective > 1.0 {
					res.Errors = append(res.Errors, fmt.Errorf("watch %s: SLO %q objective %.4f must be in (0, 1]", watchFile, slo.ID, slo.Objective))
				}
			}
		}
	}

	if len(res.Errors) > 0 {
		return res, fmt.Errorf("%w: %d validation error(s) in %s", ErrValidationFailed, len(res.Errors), dir)
	}
	return res, nil
}

// All crawls the standard profile locations under repoRoot (config/dev,
// config/thump-test, and test/onboarding/testdata/acme) and returns all results.
func All(repoRoot string) ([]ProfileResult, error) {
	profiles := []string{
		filepath.Join(repoRoot, "config", "dev"),
		filepath.Join(repoRoot, "config", "thump-test"),
		filepath.Join(repoRoot, "test", "onboarding", "testdata", "acme"),
	}

	var results []ProfileResult
	var totalErrors int

	for _, p := range profiles {
		if _, err := os.Stat(p); err == nil {
			res, err := Profile(p)
			results = append(results, res)
			if err != nil {
				totalErrors += len(res.Errors)
			}
		}
	}

	if totalErrors > 0 {
		return results, fmt.Errorf("%w: %d total error(s) across profiles", ErrValidationFailed, totalErrors)
	}
	return results, nil
}

func validateActionContract(act contract.ActionContract) []error {
	var errs []error
	if act.Name == "" {
		errs = append(errs, errors.New("empty name"))
	}
	if len(act.ApplicableFailureClasses) == 0 {
		errs = append(errs, errors.New("no applicableFailureClasses"))
	}
	for _, fc := range act.ApplicableFailureClasses {
		if !canonicalFailureClasses[fc] {
			errs = append(errs, fmt.Errorf("applicableFailureClasses has %q, not a FailureClass const", fc))
		}
	}
	if len(act.ApplicableTiers) == 0 {
		errs = append(errs, errors.New("no applicableTiers"))
	}
	if !canonicalBlastTiers[act.BlastTier] {
		errs = append(errs, fmt.Errorf("blastTier %q not in [low, med, high]", act.BlastTier))
	}
	if act.Reversal.Method == "" || act.Reversal.Fallback == "" {
		errs = append(errs, errors.New("reversal missing method or fallback"))
	}
	if len(act.Execution.Forward) == 0 {
		errs = append(errs, errors.New("no forward execution steps"))
	}
	if len(act.Execution.Reverse) == 0 {
		errs = append(errs, errors.New("no reverse execution steps"))
	}
	for i, step := range act.Execution.Forward {
		if !canonicalVerbs[step.Verb] {
			errs = append(errs, fmt.Errorf("forward step %d uses uncompiled verb %q", i, step.Verb))
		}
	}
	for i, step := range act.Execution.Reverse {
		if !canonicalVerbs[step.Verb] {
			errs = append(errs, fmt.Errorf("reverse step %d uses uncompiled verb %q", i, step.Verb))
		}
	}
	if act.SuccessCriteria.Metric == "" {
		errs = append(errs, errors.New("successCriteria missing metric"))
	}
	if act.SuccessCriteria.Window <= 0 {
		errs = append(errs, errors.New("successCriteria window must be positive duration"))
	}
	if act.SuccessCriteria.SeverityReductionPct != 0 {
		pct := act.SuccessCriteria.SeverityReductionPct
		if pct < 0 || pct > 1.0 {
			errs = append(errs, fmt.Errorf("severityReductionPct %.2f must be in [0.0, 1.0]", pct))
		}
	}
	return errs
}

func findFile(dir string, candidates ...string) string {
	for _, c := range candidates {
		p := filepath.Join(dir, c)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Main is calipers validate's entry point: parses flags, validates profile(s),
// and outputs a formatted human or JSON report.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("calipers validate", flag.ContinueOnError)
	fs.SetOutput(stderr)

	profile := fs.String("profile", "all", "Profile name to validate (dev, thump-test, acme, or all)")
	dir := fs.String("dir", "", "Explicit profile directory to validate")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	repoRoot, err := findRepoRootFromWd()
	if err != nil && *dir == "" {
		_, _ = fmt.Fprintf(stderr, "resolve repo root: %v\n", err)
		return 1
	}

	var results []ProfileResult
	if *dir != "" {
		res, err := Profile(*dir)
		results = append(results, res)
		if err != nil && len(res.Errors) == 0 {
			_, _ = fmt.Fprintf(stderr, "validate %s: %v\n", *dir, err)
			return 1
		}
	} else if *profile == "all" || *profile == "" {
		all, _ := All(repoRoot)
		results = all
	} else {
		targetDir := resolveProfileDir(repoRoot, *profile)
		res, err := Profile(targetDir)
		results = append(results, res)
		if err != nil && len(res.Errors) == 0 {
			_, _ = fmt.Fprintf(stderr, "validate %s: %v\n", targetDir, err)
			return 1
		}
	}

	hasErrors := false
	for _, r := range results {
		if len(r.Errors) > 0 {
			hasErrors = true
			_, _ = fmt.Fprintf(stderr, "FAIL: %s (%d errors)\n", r.Profile, len(r.Errors))
			for _, e := range r.Errors {
				_, _ = fmt.Fprintf(stderr, "  - %v\n", e)
			}
		} else {
			_, _ = fmt.Fprintf(stdout, "OK:   %-12s (%d actions, %d policy floors, %d SLOs, %d evidence queries)\n",
				r.Profile, r.Actions, r.PolicyFloors, r.WatchSLOs, r.EvidenceQueries)
		}
	}

	if hasErrors {
		return 1
	}
	return 0
}

func resolveProfileDir(root, profile string) string {
	switch profile {
	case "dev":
		return filepath.Join(root, "config", "dev")
	case "thump-test":
		return filepath.Join(root, "config", "thump-test")
	case "acme":
		return filepath.Join(root, "test", "onboarding", "testdata", "acme")
	default:
		if _, err := os.Stat(filepath.Join(root, "config", profile)); err == nil {
			return filepath.Join(root, "config", profile)
		}
		return profile
	}
}

func findRepoRootFromWd() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("cannot locate go.mod in parent tree")
		}
		dir = parent
	}
}
