package clank

import (
	"context"
	"encoding/json"
)

// Model is the LLM seam: the reason loop's one dependency on a provider,
// faked in tests. The concrete adaptors live in model_anthropic.go and
// model_gemini.go, each isolating its own SDK.
type Model interface {
	Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error)
}

type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema for the tool's args; nil ⇒ permissive object
}
