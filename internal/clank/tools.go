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
	// FailureClass keeps a correct diagnosis on the audit trail even when no
	// catalogued action exists for it — which classes accumulate declines is
	// the evidence catalog growth waits on. Optional, and the full enum
	// including "unknown": this is the one terminal where "nothing fits" is a
	// legal answer.
	FailureClass proposal.FailureClass `json:"failureClass,omitempty" jsonschema:"enum=dependency_saturation,enum=traffic_shift,enum=resource_exhaustion,enum=unknown,enum=redundancy_degraded,enum=service_failure,description=the failure class your evidence supports\\, if you reached a diagnosis — recorded even though no action is proposed"`
}

// InsufficientToolSpec is the model's terminal decline: the evidence supports no
// catalogued action, so the run ends with no proposal. It is offered alongside
// ProposeToolSpec because a real model can only emit a tool call for a tool it
// was given a spec for — so "stop, do nothing" must be an offered tool, not an
// assumed one.
func InsufficientToolSpec() ToolSpec {
	return ToolSpec{
		Name: "insufficient",
		Description: "Declare that no catalogued action can be proposed, and say why — name the missing evidence, " +
			"or the diagnosed failure class no catalogued action applies to.",
		InputSchema: SchemaOf[insufficientInput](),
	}
}
