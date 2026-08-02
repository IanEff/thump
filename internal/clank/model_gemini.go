package clank

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ianeff/thump/internal/reason"
	"google.golang.org/genai"
)

// GeminiModel is a second reason.Model adaptor: Gemini 2.5 Flash Lite behind the
// genai SDK, the cheapest Gemini model on record. It satisfies the same
// reason.Model interface as AnthropicModel, so the reason loop cannot tell which
// provider it's talking to. It has never completed a call against a live
// backend: Main does not select it and no test exercises it, so treat it as
// a second implementation of the interface, not a proven second provider.
type GeminiModel struct {
	client *genai.Client
}

// NewGeminiModel builds a GeminiModel authenticated with apiKey against the
// Gemini API backend.
func NewGeminiModel(ctx context.Context, apiKey string) (*GeminiModel, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}

	return &GeminiModel{client: client}, nil
}

// Complete sends msgs and tools to Gemini and folds the response into a
// reason.Completion: resp.Text becomes the assistant reason.Message, and each FunctionCall
// becomes a reason.ToolCall with its args re-marshaled to JSON, so both reason.Model
// implementations hand the engine the same reason.ToolCall shape.
func (m *GeminiModel) Complete(ctx context.Context, msgs []reason.Message, tools []reason.ToolSpec) (reason.Completion, error) {
	resp, err := m.client.Models.GenerateContent(ctx, "gemini-2.5-flash-lite", // cheapest Gemini model on record
		toGeminiContents(msgs),
		&genai.GenerateContentConfig{
			MaxOutputTokens: 4096,
			Tools:           toGeminiToolParams(tools),
		})
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
