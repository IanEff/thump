package clank

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"google.golang.org/genai"
)

type Model interface {
	Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error)
}

type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage // JSON Schema for the tool's args; nil ⇒ permissive object
}

type AnthropicModel struct {
	client anthropic.Client
}

func NewAnthropicModel(apiKey string) *AnthropicModel {
	return &AnthropicModel{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
	}
}

func (m *AnthropicModel) Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error) {
	resp, err := m.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5_20251001, // cheapest model on record
		MaxTokens: 4096,
		Messages:  toAnthropicMessageParams(msgs),
		Tools:     toAnthropicToolParams(tools),
	})
	if err != nil {
		return Completion{}, fmt.Errorf("anthropic complete: %w", err)
	}

	var comp Completion
	comp.Message.Role = "assistant"
	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			comp.Message.Content += b.Text
		case anthropic.ToolUseBlock:
			comp.ToolCalls = append(comp.ToolCalls, ToolCall{
				Name: b.Name,
				Args: json.RawMessage(b.JSON.Input.Raw()),
			})
		}
	}
	return comp, nil
}

func toAnthropicMessageParams(msgs []Message) []anthropic.MessageParam {
	params := make([]anthropic.MessageParam, 0, len(msgs))
	for _, msg := range msgs {
		block := anthropic.NewTextBlock(msg.Content)
		switch msg.Role {
		case "assistant":
			params = append(params, anthropic.NewAssistantMessage(block))
		default: // "user", "tool"
			params = append(params, anthropic.NewUserMessage(block))
		}
	}
	return params
}

func toAnthropicToolParams(tools []ToolSpec) []anthropic.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}

	params := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		tool := anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: toAnthropicInputSchema(t.InputSchema),
		}
		params = append(params, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return params
}

// toAnthropicInputSchema adapts a raw JSON Schema into the SDK's param shape: "properties"
// fills Properties, and the rest (e.g. "required") rides in ExtraFields. A nil or
// unparseable schema falls back to a permissive object, so schemaless tools still
// work — only structured tools like propose need the full document.
func toAnthropicInputSchema(raw json.RawMessage) anthropic.ToolInputSchemaParam {
	schema := anthropic.ToolInputSchemaParam{Properties: map[string]any{}}
	if len(raw) == 0 {
		return schema
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return schema
	}
	if props, ok := doc["properties"]; ok {
		schema.Properties = props
	}
	extra := map[string]any{}
	for k, v := range doc {
		switch k {
		case "type", "properties", "$schema", "$id":
			// type is a constant the SDK sets; the others aren't request fields.
		default:
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		schema.ExtraFields = extra
	}
	return schema
}

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
