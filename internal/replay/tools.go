package replay

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/reason"
)

// recordedTool answers every call with the EvidenceRefs the recorded run
// produced for that backend, in order — it never reaches a backend and never
// reconstructs a field: an EvidenceRef here is the one the live run emitted,
// carried whole on the proposal.Set.
type recordedTool struct {
	name string
	refs []proposal.EvidenceRef
	pos  int
}

func (t *recordedTool) Spec() reason.ToolSpec {
	return reason.ToolSpec{
		Name:        t.name,
		Description: "replayed evidence from a recorded run",
	}
}

func (t *recordedTool) Run(_ context.Context, _ json.RawMessage) (proposal.EvidenceRef, error) {
	if t.pos >= len(t.refs) {
		return proposal.EvidenceRef{}, fmt.Errorf("%w: tool %q", ErrTranscriptExhausted, t.name)
	}
	ref := t.refs[t.pos]
	t.pos++
	return ref, nil
}

// BuildTools groups the recorded run's evidence by the backend that produced
// it, so a replayed loop calling "metrics" gets back the metrics refs in the
// order the live run got them. Grouping by Tool is deliberate — Tool is the
// dedup key the ≥2-backend grounding floor counts on, so preserving it is
// what makes the replayed confidence the same number.
func BuildTools(set proposal.Set) map[string]reason.Tool {
	byTool := make(map[string][]proposal.EvidenceRef)
	for _, ref := range set.Evidence {
		byTool[ref.Tool] = append(byTool[ref.Tool], ref)
	}

	tools := make(map[string]reason.Tool, len(byTool))
	for name, refs := range byTool {
		tools[name] = &recordedTool{name: name, refs: refs}
	}

	return tools
}
