package clank

import (
	"context"
	"encoding/json"

	"github.com/ianeff/thump/api/v1/proposal"
)

// Tool is a read-only evidence source the reason loop can call mid-turn —
// telemetry, logs, cluster state, or case-base retrieval. Run returns a
// proposal.EvidenceRef: a one-line digest plus a backend ref to re-fetch,
// never the raw payload. EvidenceRef has no Raw field and never will — raw
// data cannot enter the conversation history the model reasons over.
type Tool interface {
	Spec() ToolSpec
	Run(ctx context.Context, args json.RawMessage) (proposal.EvidenceRef, error)
}

// ProposeToolSpec is the model's terminal `propose` tool: the leading
// proposal.FailureClass, the competing hypotheses, and the candidate actions (each drawn
// from the catalog). Its input schema is generated from proposeInput, so the
// shape the model is held to is the shape the engine decodes.
func ProposeToolSpec() ToolSpec {
	return ToolSpec{
		Name:        "propose",
		Description: "Emit your final answer: the leading failure class, the competing hypotheses, and the candidate actions, each drawn from the action catalog.",
		InputSchema: SchemaOf[proposeInput](),
	}
}

type insufficientInput struct {
	Reason string `json:"reason" jsonschema:"required"`
}

// InsufficientToolSpec is the model's terminal decline: the evidence supports no
// catalogued action, so the run ends with no proposal. It is offered alongside
// ProposeToolSpec because a real model can only emit a tool call for a tool it
// was given a spec for — so "stop, do nothing" must be an offered tool, not an
// assumed one. No input schema: it takes no arguments the engine reads.
func InsufficientToolSpec() ToolSpec {
	return ToolSpec{
		Name: "insufficient",
		Description: "Declare the evidence insufficient to propose any catalogued action," +
			"and say why - name the missing evidency or why no catalogued action fits.",
		InputSchema: SchemaOf[insufficientInput](),
	}
}
