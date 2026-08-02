package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/ianeff/thump/internal/schema"
)

type fixture struct {
	Name string `json:"name" jsonschema:"required"`
}

// TestOf_ReflectsARequiredFieldOntoTheGeneratedDocument pins the one
// thing every tool spec builder relies on: a jsonschema:"required" tag on T
// survives reflection into the document's own "required" list, so the
// contract sent to the model and the struct its reply decodes into can never
// disagree about what's mandatory.
func TestOf_ReflectsARequiredFieldOntoTheGeneratedDocument(t *testing.T) {
	t.Parallel()

	got := schema.Of[fixture]()
	if got == nil {
		t.Fatal("Of returned nil for a reflectable type")
	}

	var doc struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("Of produced invalid JSON: %v", err)
	}

	if _, ok := doc.Properties["name"]; !ok {
		t.Errorf("schema properties %v missing \"name\"", doc.Properties)
	}
	found := false
	for _, r := range doc.Required {
		if r == "name" {
			found = true
		}
	}
	if !found {
		t.Errorf("schema required list %v missing \"name\"", doc.Required)
	}
}
