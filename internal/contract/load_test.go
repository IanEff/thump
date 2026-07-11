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
