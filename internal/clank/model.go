package clank

import (
	"context"
	"encoding/json"
)

// Model is the LLM seam: the reason loop's one dependency on a provider,
// faked in every test with a scripted Completion sequence. Complete is the
// whole interface — no streaming, no state kept between calls beyond msgs —
// so a fake needs only a queue of Completions to drive the loop
// deterministically. The concrete adaptors (AnthropicModel, GeminiModel) live
// in model_anthropic.go and model_gemini.go, each isolating its own SDK.
type Model interface {
	Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error)
}

// ToolSpec is what a Tool tells the model about itself: a name, a
// description, and a JSON Schema for its args. The engine offers a fixed set
// of these every turn — the read-only tools plus the two terminal verbs,
// ProposeToolSpec and InsufficientToolSpec — and a model can only call a tool
// it was offered one of these for.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema for the tool's args; nil ⇒ permissive object
}
