package contract_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
)

// TestLoadCatalog_RoundTripsDefaultData is the loader's drift guard: marshal
// the compiled Default() catalog to YAML and reload it, and every data field
// must come back identical. Only Precondition.OK is excluded — it's
// yaml:"-" by construction and never round-trips; Load rebinds it from a
// registry instead of the wire, proven separately below.
func TestLoadCatalog_RoundTripsDefaultData(t *testing.T) {
	want := contract.Default().Contracts()

	raw, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("marshal Default(): %v", err)
	}

	got, err := contract.Load(raw, contract.PreconditionRegistry{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if diff := cmp.Diff(want, got.Contracts(), cmpopts.IgnoreFields(contract.Precondition{}, "OK")); diff != "" {
		t.Errorf("catalog round-trip (-want +got):\n%s", diff)
	}
}

const preconditionFixture = `
- name: test-contract
  applicableTiers: [tier-1]
  preconditions:
    - name: always-true
`

// TestLoadCatalog_BindsPreconditionByName proves the C3 seam: a YAML
// Precondition carries only a Name, and Load binds its OK func by looking
// that name up in the passed registry — the file supplies the name, only
// code supplies the check.
func TestLoadCatalog_BindsPreconditionByName(t *testing.T) {
	reg := contract.PreconditionRegistry{
		"always-true": func(proposal.SAO) bool { return true },
	}

	got, err := contract.Load([]byte(preconditionFixture), reg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	c, ok := got.ByName("test-contract")
	if !ok {
		t.Fatal("loaded catalog has no test-contract")
	}
	if len(c.Preconditions) != 1 || c.Preconditions[0].OK == nil {
		t.Fatalf("test-contract's precondition wasn't bound: %+v", c.Preconditions)
	}
	if !c.Preconditions[0].OK(proposal.SAO{}) {
		t.Error("bound precondition should evaluate true, got false")
	}
}

const unknownPreconditionFixture = `
- name: test-contract
  applicableTiers: [tier-1]
  preconditions:
    - name: never-registered
`

// TestLoadCatalog_UnknownPrecondition_Errors proves a contract naming a
// precondition the registry doesn't have is a load error — never a nil OK
// func silently waiting to nil-pointer-panic the first time preconditionsMet
// calls it.
func TestLoadCatalog_UnknownPrecondition_Errors(t *testing.T) {
	_, err := contract.Load([]byte(unknownPreconditionFixture), contract.PreconditionRegistry{})
	if err == nil {
		t.Fatal("Load with an unregistered precondition name: want an error, got nil")
	}
}

// TestShippedCatalogMatchesAuthoredDefault is C3's drift guard, the same
// shape as C2's TestCephLabWatch_MatchesTheLabContract: the checked-in
// config/actions/catalog.yaml — not a compiled-in literal anymore — must
// still declare exactly the authored action set. If this goes red after
// hand-editing the YAML, that's the guard working.
func TestShippedCatalogMatchesAuthoredDefault(t *testing.T) {
	got, err := contract.LoadCatalogFile("../../config/actions/catalog.yaml", contract.Preconditions)
	if err != nil {
		t.Fatalf("LoadCatalogFile: %v", err)
	}

	want := contract.Default().Contracts()
	if diff := cmp.Diff(want, got.Contracts(), cmpopts.IgnoreFields(contract.Precondition{}, "OK")); diff != "" {
		t.Errorf("config/actions/catalog.yaml drifted from contract.Default() (-want +got):\n%s", diff)
	}
}

// TestDefault_DependencySaturationOffersTwoDistinctRemedies is E2's
// discrimination pin: a failure class served by exactly one authored action
// is a rubber-stamp, not a choice — the model has nothing to weigh.
// dependency_saturation must offer both a load-shedding remedy
// (throttle-non-critical-paths) and a capacity remedy
// (scale-out-rgw-gateways), each independently reversible, so ranking a
// dependency_saturation proposal is a real trade-off (Seam 3's causal
// scorer/ranker have two candidates to compare), not a formality.
func TestDefault_DependencySaturationOffersTwoDistinctRemedies(t *testing.T) {
	got := contract.Default().Applicable(proposal.ClassDependencySaturation, "tier-1", proposal.SAO{})

	var names []string
	for _, c := range got {
		names = append(names, c.Name)
		if c.Reversal.Method == "" {
			t.Errorf("%s has no reversal method — every dependency_saturation action must be reversible", c.Name)
		}
	}

	want := []string{"throttle-non-critical-paths", "scale-out-rgw-gateways"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("dependency_saturation's applicable actions (-want +got):\n%s", diff)
	}
}

// TestDefault_RedundancyDegradedOffersHoldRebalanceWithAForecast pins I2's
// realignment: hold-rebalance is reachable under redundancy_degraded
// (relabeled off resource_exhaustion, thump-running-notes.md 2026-07-17 part
// 9), and it carries the SeverityQuery/SeverityReductionPct pair
// recordEffectiveness needs — a contract with no SeverityReductionPct feeds
// the effectiveness delta no forecast to score against. redundancy_degraded
// now offers two independently reversible remedies, the same discrimination
// shape dependency_saturation has above.
func TestDefault_RedundancyDegradedOffersHoldRebalanceWithAForecast(t *testing.T) {
	got := contract.Default().Applicable(proposal.ClassRedundancyDegraded, "tier-1", proposal.SAO{})

	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	want := []string{"hold-rebalance", "accelerate-recovery"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("redundancy_degraded's applicable actions (-want +got):\n%s", diff)
	}

	holdRebalance, ok := contract.Default().ByName("hold-rebalance")
	if !ok {
		t.Fatal("hold-rebalance is not in the catalog")
	}
	if holdRebalance.SuccessCriteria.SeverityReductionPct == 0 {
		t.Error("hold-rebalance needs a non-zero SeverityReductionPct or the effectiveness delta has no forecast to score")
	}
	if holdRebalance.SuccessCriteria.SeverityQuery == "" {
		t.Error("hold-rebalance needs a SeverityQuery so the post-action check has an axis to read")
	}
}

// TestShippedFailureClassesMatchesAuthoredDefault is the failure-class
// definitions' drift guard, the same shape as
// TestShippedCatalogMatchesAuthoredDefault: the checked-in
// config/actions/failure-classes.yaml must still declare exactly the
// authored set clank's seedPrompt renders to the model.
func TestShippedFailureClassesMatchesAuthoredDefault(t *testing.T) {
	got, err := contract.LoadFailureClassesFile("../../config/actions/failure-classes.yaml")
	if err != nil {
		t.Fatalf("LoadFailureClassesFile: %v", err)
	}

	if diff := cmp.Diff(contract.DefaultFailureClasses(), got); diff != "" {
		t.Errorf("config/actions/failure-classes.yaml drifted from contract.DefaultFailureClasses() (-want +got):\n%s", diff)
	}
}

// TestDefaultFailureClasses_CoversEveryFailureClass is the completeness
// guard: proposal.FailureClass is a closed enum, but Go can't enumerate a
// type's consts by reflection, so this test hardcodes the canonical set
// once and fails loudly if DefaultFailureClasses() and this list ever
// disagree — exactly the failure mode a future added-but-undefined class
// would otherwise hit silently (a class the model can declare but was never
// told the meaning of).
func TestDefaultFailureClasses_CoversEveryFailureClass(t *testing.T) {
	want := map[proposal.FailureClass]bool{
		proposal.ClassDependencySaturation: true,
		proposal.ClassTrafficShift:         true,
		proposal.ClassResourceExhaustion:   true,
		proposal.ClassRedundancyDegraded:   true,
		proposal.ClassUnknown:              true,
	}
	got := map[proposal.FailureClass]bool{}
	for _, d := range contract.DefaultFailureClasses() {
		if d.Description == "" {
			t.Errorf("%q has an empty Description", d.Class)
		}
		got[d.Class] = true
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("DefaultFailureClasses() does not cover exactly proposal's FailureClass consts (-want +got):\n%s", diff)
	}
}
