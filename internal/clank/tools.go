package clank

import (
	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/reason"
	"github.com/ianeff/thump/internal/schema"
)

// ProposeToolSpec is the model's terminal `propose` tool: the leading
// proposal.FailureClass, the competing hypotheses, and the candidate actions (each drawn
// from the catalog). Its input schema is generated from proposeInput, so the
// shape the model is held to is the shape the engine decodes.
func ProposeToolSpec() reason.ToolSpec {
	return reason.ToolSpec{
		Name:        "propose",
		Description: "Emit your final answer: the leading failure class, the competing hypotheses, and the candidate actions, each drawn from the action catalog.",
		InputSchema: schema.Of[proposeInput](),
	}
}

// proposeInput is the wire shape of the model's terminal `propose` tool call:
// the subset of a proposal.Set the LLM authors (the engine fills the rest). It is
// the single source for both the tool's input schema and — once the engine is
// wired — the json.Unmarshal target, so the two can't disagree.
type proposeInput struct {
	// The enum mirrors the proposal.FailureClass constants minus "unknown" —
	// diagnosable, never proposable: no catalogued action may list it, so its
	// only terminal is the insufficient tool, and offering the token here
	// hands the model a string with no legal use in a propose call. The
	// propose_schema.json golden pins the emitted shape.
	FailureClass proposal.FailureClass `json:"failureClass" jsonschema:"required,enum=dependency_saturation,enum=traffic_shift,enum=resource_exhaustion,enum=redundancy_degraded,enum=service_failure"`
	Hypotheses   []proposal.Hypothesis `json:"hypotheses,omitempty"`
	Proposals    []proposeCandidate    `json:"proposals" jsonschema:"required"`
}

// proposeCandidate is the LLM-authored slice of a proposal.Candidate: a catalogued action
// (contractRef) with a hypothesis confidence. Everything else a proposal.Candidate carries
// — predicted impact, reversal path, governance band, rank — is the catalog's,
// the ranker's, or deferred, so it is deliberately absent from what the model may
// author. The json tags mirror proposal.Candidate's, so it decodes straight into one.
type proposeCandidate struct {
	ID          string `json:"id,omitempty"`
	ContractRef string `json:"contractRef" jsonschema:"required"`
	// Confidence is a pointer because an unstated ceiling and a stated 0.0 are
	// different claims — nil means the model asserted no ceiling, and
	// candidates() translates it to 1.0, min's identity. Required so a model
	// normally commits to a number, pointer so one that doesn't cannot zero
	// every candidate in the set.
	Confidence *float64 `json:"confidence" jsonschema:"required"`
	// Citations must repeat cite keys verbatim — the engine validates them by
	// exact string match against the keys it showed, so a paraphrase or a
	// description of the value is an auditable decline, not a near miss.
	Citations []string `json:"citations" jsonschema:"required,description=the evidence backing this candidate: the exact keys shown as [cite: <key>] in this run's tool results\\, repeated verbatim — never a description or paraphrase of the value"`
}

// candidates converts what the model authored into boundary objects, resolving
// an unstated confidence to 1.0 — the identity of the min() the scorer applies
// it as, where the zero value would be its annihilator and would drive every
// candidate in the set to zero regardless of what the run actually grounded.
func (p proposeInput) candidates() []proposal.Candidate {
	out := make([]proposal.Candidate, 0, len(p.Proposals))
	for _, c := range p.Proposals {
		ceiling := 1.0
		if c.Confidence != nil {
			ceiling = *c.Confidence
		}
		out = append(out, proposal.Candidate{
			ID:          c.ID,
			ContractRef: c.ContractRef,
			Confidence:  ceiling,
			Citations:   c.Citations,
		})
	}
	return out
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
func InsufficientToolSpec() reason.ToolSpec {
	return reason.ToolSpec{
		Name: "insufficient",
		Description: "Declare that no catalogued action can be proposed, and say why — name the missing evidence, " +
			"or the diagnosed failure class no catalogued action applies to.",
		InputSchema: schema.Of[insufficientInput](),
	}
}
