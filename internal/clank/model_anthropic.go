package clank

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicModel is the production Model: Claude Haiku behind the Messages
// API, the cheapest model on record for this loop. It's the adaptor Main
// wires in — GeminiModel exists as a second Model implementation but Main
// doesn't select it yet.
type AnthropicModel struct {
	client anthropic.Client
}

// NewAnthropicModel builds an AnthropicModel authenticated with apiKey.
func NewAnthropicModel(apiKey string) *AnthropicModel {
	return &AnthropicModel{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
	}
}

// Complete sends msgs and tools to Claude Haiku and folds the response into
// a Completion: text blocks concatenate into the assistant Message, and each
// ToolUseBlock becomes a ToolCall. A tool the model wasn't offered in tools
// can never come back here — the SDK only echoes tool calls for tools it was
// given a spec for.
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
