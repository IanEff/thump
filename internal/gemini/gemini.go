// Package gemini is a second reason.Model adaptor: Gemini behind the genai
// SDK, isolating it from clank the same way internal/anthropic isolates the
// Anthropic SDK. clank.Main never selects it — only calipers probe's
// -model=gemini-low does, for measurement, never for production reasoning.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ianeff/thump/internal/reason"
	"google.golang.org/genai"
)

// ModelGemini3_5FlashLite is Gemini's currently cheapest 3.x-family model —
// the one candidate in this package with ThinkingLevel support (2.5-family
// models take a ThinkingBudget token count instead).
const ModelGemini3_5FlashLite = "gemini-3.5-flash-lite"

// Model is a second reason.Model adaptor: Gemini behind the genai SDK. It
// satisfies the same reason.Model interface as internal/anthropic's Model,
// so the reason loop cannot tell which provider it's talking to.
type Model struct {
	client        *genai.Client
	model         string
	thinkingLevel genai.ThinkingLevel
}

// NewModel builds a Model authenticated with apiKey against the Gemini API
// backend, sending model and thinkingLevel on every call. A zero
// thinkingLevel omits ThinkingConfig entirely — only pass one of the SDK's
// named levels (e.g. genai.ThinkingLevelLow) for a model that supports it.
func NewModel(ctx context.Context, apiKey, model string, thinkingLevel genai.ThinkingLevel) (*Model, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}

	return &Model{client: client, model: model, thinkingLevel: thinkingLevel}, nil
}

// Complete sends msgs and tools to m's configured model and folds the
// response into a reason.Completion: resp.Text becomes the assistant
// reason.Message, and each FunctionCall becomes a reason.ToolCall with its
// args re-marshaled to JSON, so both reason.Model implementations hand the
// engine the same reason.ToolCall shape.
func (m *Model) Complete(ctx context.Context, msgs []reason.Message, tools []reason.ToolSpec) (reason.Completion, error) {
	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens: 4096,
		Tools:           toGeminiToolParams(tools),
	}
	if m.thinkingLevel != "" {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: m.thinkingLevel}
	}
	resp, err := m.client.Models.GenerateContent(ctx, m.model, toGeminiContents(msgs), cfg)
	if err != nil {
		return reason.Completion{}, fmt.Errorf("gemini complete: %w", err)
	}

	var comp reason.Completion
	comp.Message.Role = "assistant"
	comp.Message.Content = resp.Text()
	for _, fc := range resp.FunctionCalls() {
		args, err := json.Marshal(fc.Args)
		if err != nil {
			return reason.Completion{}, fmt.Errorf("gemini marshal tool args: %w", err)
		}
		comp.ToolCalls = append(comp.ToolCalls, reason.ToolCall{ID: fc.ID, Name: fc.Name, Args: args})
	}
	return comp, nil
}

func toGeminiContents(msgs []reason.Message) []*genai.Content {
	contents := make([]*genai.Content, 0, len(msgs))
	for _, msg := range msgs {
		var parts []*genai.Part
		if msg.Content != "" {
			parts = append(parts, genai.NewPartFromText(msg.Content))
		}
		for _, c := range msg.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal(c.Args, &args); err != nil {
				args = map[string]any{}
			}
			parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
				ID: c.ID, Name: c.Name, Args: args,
			}})
		}
		for _, r := range msg.ToolResults {
			parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{
				ID: r.CallID, Name: r.Name, Response: map[string]any{"output": r.Digest},
			}})
		}
		if len(parts) == 0 {
			continue
		}
		role := genai.Role(genai.RoleUser)
		if msg.Role == "assistant" {
			role = genai.RoleModel
		}
		contents = append(contents, genai.NewContentFromParts(parts, role))
	}
	return contents
}

func toGeminiToolParams(tools []reason.ToolSpec) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: toGeminiParametersSchema(t.InputSchema),
		})
	}
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// toGeminiParametersSchema passes the raw JSON Schema through as-is: unlike Anthropic's
// SDK, genai's ParametersJsonSchema takes the whole document (any), so there's no
// properties/required split to reassemble. A nil or unparseable schema leaves the
// declaration's Parameters unset, which the SDK documents as valid for no-arg functions.
func toGeminiParametersSchema(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil
	}
	return schema
}
