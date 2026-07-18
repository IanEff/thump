package clank

import (
	"encoding/json"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/invopop/jsonschema"
)

// SchemaOf reflects T into a JSON Schema document. Tool input schemas are
// generated from the Go types the model must populate, so the contract we send
// the model and the type we json.Unmarshal its reply into can never drift.
//
// DoNotReference inlines nested types (no $defs) and ExpandedStruct hoists T's
// fields to the root object — the shape the Messages API wants for input_schema.
func SchemaOf[T any]() json.RawMessage {
	r := jsonschema.Reflector{DoNotReference: true, ExpandedStruct: true}
	b, err := json.Marshal(r.Reflect(new(T)))
	if err != nil {
		return nil
	}
	return b
}

// proposeInput is the wire shape of the model's terminal `propose` tool call:
// the subset of a proposal.Set the LLM authors (the engine fills the rest). It is
// the single source for both the tool's input schema and — once the engine is
// wired — the json.Unmarshal target, so the two can't disagree.
type proposeInput struct {
	// The enum mirrors the proposal.FailureClass constants; the
	// propose_schema.json golden pins the emitted shape.
	FailureClass proposal.FailureClass `json:"failureClass" jsonschema:"required,enum=dependency_saturation,enum=traffic_shift,enum=resource_exhaustion,enum=unknown,enum=redundancy_degraded"`
	Hypotheses   []proposal.Hypothesis `json:"hypotheses,omitempty"`
	Proposals    []proposeCandidate    `json:"proposals" jsonschema:"required"`
}

// proposeCandidate is the LLM-authored slice of a proposal.Candidate: a catalogued action
// (contractRef) with a hypothesis confidence. Everything else a proposal.Candidate carries
// — predicted impact, reversal path, governance band, rank — is the catalog's,
// the ranker's, or deferred, so it is deliberately absent from what the model may
// author. The json tags mirror proposal.Candidate's, so it decodes straight into one.
type proposeCandidate struct {
	ID          string  `json:"id,omitempty"`
	ContractRef string  `json:"contractRef" jsonschema:"required"`
	Confidence  float64 `json:"confidence,omitempty"`
}
