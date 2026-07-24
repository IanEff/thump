package contract_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/contract"
)

// TestLoadCatalog_RoundTripsShippedData is the codec's guard: marshal the
// shipped catalog back to YAML and reload it, and every data field must come
// back identical. Only Precondition.OK is excluded — it's yaml:"-" by
// construction and never round-trips; Load rebinds it from a registry
// instead of the wire, proven separately below.
func TestLoadCatalog_RoundTripsShippedData(t *testing.T) {
	want := loadShippedCatalog(t).Contracts()
	raw, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("marshal shipped catalog: %v", err)
	}

	got, err := contract.Load(raw, contract.PreconditionRegistry{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if diff := cmp.Diff(want, got.Contracts(), cmpopts.IgnoreFields(contract.Precondition{}, "OK")); diff != "" {
		t.Error("catalog round-trip lost or changed a field", diff)
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

// TestShippedCatalog_RedundancyDegradedOffersHoldRebalanceWithAForecast pins
// that hold-rebalance is reachable under redundancy_degraded rather than
// resource_exhaustion, and that it carries the SeverityQuery/
// SeverityReductionPct pair recordEffectiveness needs — a contract with no
// SeverityReductionPct feeds the effectiveness delta no forecast to score
// against. redundancy_degraded offers two independently reversible remedies,
// the same discrimination shape dependency_saturation has.
func TestShippedCatalog_RedundancyDegradedOffersHoldRebalanceWithAForecast(t *testing.T) {
	cat := loadShippedCatalog(t)

	got := cat.Applicable(proposal.ClassRedundancyDegraded, "tier-1", proposal.SAO{})

	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	want := []string{"hold-rebalance", "accelerate-recovery"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Error("redundancy_degraded's applicable actions", diff)
	}

	holdRebalance, ok := cat.ByName("hold-rebalance")
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

// TestShippedFailureClasses_DefineEveryFailureClass is the completeness
// guard on what clank's seed prompt tells the model: a class the model may
// declare but was never given the meaning of is a class it will guess at.
func TestShippedFailureClasses_DefineEveryFailureClass(t *testing.T) {
	t.Parallel()

	want := canonicalFailureClasses()
	got := map[proposal.FailureClass]bool{}
	for _, d := range loadShippedFailureClasses(t) {
		if d.Description == "" {
			t.Errorf("%q has an empty description — the model is told the class exists, not what it means", d.Class)
		}
		got[d.Class] = true
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("config/actions/failure-classes.yaml does not define exactly proposal's FailureClass consts", diff)
	}
}
