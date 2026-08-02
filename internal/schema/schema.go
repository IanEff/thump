// Package schema reflects Go types into JSON Schema documents for the
// Messages API's tool input_schema — the contract sent to the model and the
// type its reply is json.Unmarshal'd into are the same Go type, so they can
// never drift apart.
package schema

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// Of reflects T into a JSON Schema document. DoNotReference inlines
// nested types (no $defs) and ExpandedStruct hoists T's fields to the root
// object — the shape the Messages API wants for input_schema.
func Of[T any]() json.RawMessage {
	r := jsonschema.Reflector{DoNotReference: true, ExpandedStruct: true}
	b, err := json.Marshal(r.Reflect(new(T)))
	if err != nil {
		return nil
	}
	return b
}
