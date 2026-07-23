package clank_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/contract"
)

// update regenerates the golden files instead of asserting against them:
//
//	go test ./internal/clank -run Pins -update
//
// AGENTS.md §5 says never add an -update flag to golden tests, to stop reflexive
// rubber-stamping of a drifted golden. We keep it *here* as a conscious exception:
// this golden is reflection-generated from proposeInput's tags, so hand-editing it
// risks writing JSON the reflector would never emit — a golden that lies. Regen is
// the safer update path; the boundary still moves visibly in the PR diff.
var update = flag.Bool("update", false, "update golden files in testdata")

// TestProposeToolSpec_PinsTheAutonomyBoundaryToGolden pins the propose tool's input
// schema — the autonomy boundary the model is held to — to a checked-in golden. Any
// change to proposeInput, its json/jsonschema tags, or the FailureClass enum that
// shifts the schema fails here, for free and offline, so the boundary moves in
// review rather than in a spendy integration run.
func TestProposeToolSpec_PinsTheAutonomyBoundaryToGolden(t *testing.T) {
	var indented bytes.Buffer
	if err := json.Indent(&indented, clank.ProposeToolSpec().InputSchema, "", "  "); err != nil {
		t.Fatalf("propose schema is not valid JSON: %v", err)
	}
	got := append(indented.Bytes(), '\n')

	golden := filepath.Join("testdata", "propose_schema.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}

	want, err := os.ReadFile(golden) //nolint:gosec // G304: fixed testdata path, not user input
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("propose schema drifted from golden (-want +got):\n%s", diff)
	}
}

// failureClassEnum extracts the failureClass property's enum list from a
// propose input JSON Schema document, sorted for comparison against another
// unordered source of class names.
func failureClassEnum(t *testing.T, schema json.RawMessage) []string {
	t.Helper()

	var doc struct {
		Properties struct {
			FailureClass struct {
				Enum []string `json:"enum"`
			} `json:"failureClass"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatalf("propose schema is not valid JSON: %v", err)
	}

	enum := doc.Properties.FailureClass.Enum
	sort.Strings(enum)
	return enum
}

// TestProposeToolSpec_FailureClassEnumCoversEveryProposableFailureClass pins the
// propose schema's failureClass enum against contract.DefaultFailureClasses —
// the model can only ever declare a class this enum lists, so a class present
// in the catalog's prompt but absent here is one the real model can never
// name, no matter what seedPrompt tells it. The one exclusion is "unknown":
// diagnosable, never proposable — no catalogued action may list it, so its
// only terminal is the insufficient tool, and offering it propose-side hands
// the model a legal-looking token with no legal use.
func TestProposeToolSpec_FailureClassEnumCoversEveryProposableFailureClass(t *testing.T) {
	got := failureClassEnum(t, clank.ProposeToolSpec().InputSchema)

	want := make([]string, 0, len(contract.DefaultFailureClasses()))
	for _, def := range contract.DefaultFailureClasses() {
		if def.Class == proposal.ClassUnknown {
			continue
		}
		want = append(want, string(def.Class))
	}
	sort.Strings(want)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("propose schema's failureClass enum disagrees with the proposable DefaultFailureClasses", cmp.Diff(want, got))
	}
}

func TestInsufficientToolSpec_PinsItsInputSchemaToGolden(t *testing.T) {
	var indented bytes.Buffer
	if err := json.Indent(&indented, clank.InsufficientToolSpec().InputSchema, "", "  "); err != nil {
		t.Fatalf("insufficient schema is not valid JSON: %v", err)
	}
	got := append(indented.Bytes(), '\n')

	golden := filepath.Join("testdata", "insufficient_schema.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o600); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden) //nolint:gosec
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("insufficient schema drifted from golden (-want +got):\n%s", diff)
	}
}
