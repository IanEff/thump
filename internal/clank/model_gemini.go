package clank

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/genai"
)

type GeminiModel struct {
	client *genai.Client
}

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

func (m *GeminiModel) Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error) {
	resp, err := m.client.Models.GenerateContent(ctx, "gemini-2.5-flash-lite", // cheapest Gemini model on record
		toGeminiContents(msgs),
		&genai.GenerateContentConfig{
			MaxOutputTokens: 4096,
			Tools:           toGeminiToolParams(tools),
		})
	if err != nil {
		return Completion{}, fmt.Errorf("gemini complete: %w", err)
	}

	var comp Completion
	comp.Message.Role = "assistant"
	comp.Message.Content = resp.Text()
	for _, fc := range resp.FunctionCalls() {
		args, err := json.Marshal(fc.Args)
		if err != nil {
			return Completion{}, fmt.Errorf("gemini marshal tool args: %w", err)
		}
		comp.ToolCalls = append(comp.ToolCalls, ToolCall{Name: fc.Name, Args: args})
	}
	return comp, nil
}

// toGeminiContents mirrors toAnthropicMessageParams: only "assistant" turns are the
// model's own, everything else (user, tool results) rides back in as a user turn.
func toGeminiContents(msgs []Message) []*genai.Content {
	contents := make([]*genai.Content, 0, len(msgs))
	for _, msg := range msgs {
		var role genai.Role = genai.RoleUser
		if msg.Role == "assistant" {
			role = genai.RoleModel
		}
		contents = append(contents, genai.NewContentFromText(msg.Content, role))
	}
	return contents
}

func toGeminiToolParams(tools []ToolSpec) []*genai.Tool {
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
