package clank

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ianeff/thump/internal/reason"
)

// modelRequestTimeout bounds one call to the model. Longer than
// httpx.DefaultBackendTimeout on purpose — a Haiku completion carrying tool
// results legitimately runs longer than a PromQL query, and this is the
// reason loop's own call, not a telemetry read.
const modelRequestTimeout = 120 * time.Second

// AnthropicModel is the production reason.Model: Claude Haiku behind the Messages
// API, the cheapest model on record for this loop. It's the adaptor Main
// wires in — GeminiModel exists as a second reason.Model implementation but Main
// doesn't select it yet.
type AnthropicModel struct {
	client anthropic.Client
}

// NewAnthropicModel builds an AnthropicModel authenticated with apiKey.
func NewAnthropicModel(apiKey string) *AnthropicModel {
	return &AnthropicModel{
		client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithRequestTimeout(modelRequestTimeout)),
	}
}

// Complete sends msgs and tools to Claude Haiku and folds the response into
// a reason.Completion via fromAnthropicMessage. A tool the model wasn't offered in
// tools can never come back here — the SDK only echoes tool calls for tools
// it was given a spec for.
func (m *AnthropicModel) Complete(ctx context.Context, msgs []reason.Message, tools []reason.ToolSpec) (reason.Completion, error) {
	resp, err := m.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5_20251001, // cheapest model on record
		MaxTokens: 4096,
		Messages:  toAnthropicMessageParams(msgs),
		Tools:     toAnthropicToolParams(tools),
	})
	if err != nil {
		return reason.Completion{}, fmt.Errorf("anthropic complete: %w", err)
	}
	return fromAnthropicMessage(resp), nil
}

// toAnthropicMessageParams renders msgs into the SDK's wire shape. Role
// follows the API's turn model, not reason.Message.Role directly — a tool turn
// carries a tool_result block, and tool_result is defined as a user block, so
// "tool" renders as MessageParamRoleUser alongside plain "user" turns.
func toAnthropicMessageParams(msgs []reason.Message) []anthropic.MessageParam {
	params := make([]anthropic.MessageParam, 0, len(msgs))
	for _, msg := range msgs {
		var blocks []anthropic.ContentBlockParamUnion
		if msg.Content != "" {
			blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
		}
		for _, c := range msg.ToolCalls {
			blocks = append(blocks, anthropic.NewToolUseBlock(c.ID, toolInput(c.Args), c.Name))
		}
		for _, r := range msg.ToolResults {
			blocks = append(blocks, anthropic.NewToolResultBlock(r.CallID, r.Digest, r.IsError))
		}
		if len(blocks) == 0 {
			continue
		}
		if msg.Role == "assistant" {
			params = append(params, anthropic.NewAssistantMessage(blocks...))
		} else {
			params = append(params, anthropic.NewUserMessage(blocks...))
		}
	}
	if len(params) > 0 {
		last := &params[len(params)-1]
		if n := len(last.Content); n > 0 {
			setCacheControl(last.Content[n-1])
		}
	}
	return params
}

// cacheControlHolder is satisfied by anthropic.ContentBlockParamUnion and
// anthropic.ToolUnionParam — every wire-shape union toAnthropicMessageParams
// and toAnthropicToolParams emit.
type cacheControlHolder interface {
	GetCacheControl() *anthropic.CacheControlEphemeralParam
}

// setCacheControl marks v as a prompt-cache breakpoint. GetCacheControl
// returns nil only if v's union has no variant set at all, which never
// happens for a block or tool this package actually constructs.
func setCacheControl(v cacheControlHolder) {
	if cc := v.GetCacheControl(); cc != nil {
		*cc = anthropic.NewCacheControlEphemeralParam()
	}
}

// toolInput decodes a call's raw args for the SDK, falling back to an empty
// object rather than nil — Input is a required field and a nil value is
// omitted from the request entirely.
func toolInput(args json.RawMessage) any {
	var input any
	if len(args) == 0 || json.Unmarshal(args, &input) != nil {
		return map[string]any{}
	}
	return input
}

func toAnthropicToolParams(tools []reason.ToolSpec) []anthropic.ToolUnionParam {
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
	// The catalog is the same five tools, unchanged, every turn within a run —
	// the one breakpoint most likely to actually clear the cache floor, once
	// toolSpecs' sort keeps the rendered order byte-identical turn to turn.
	setCacheControl(params[len(params)-1])
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

// fromAnthropicMessage folds resp into a reason.Completion — text blocks concatenate
// into the assistant reason.Message, each ToolUseBlock becomes a reason.ToolCall, and
// resp.Usage always maps onto reason.Completion.Usage, zero fields and all, since
// the SDK reports it as a required struct rather than an optional one.
func fromAnthropicMessage(resp *anthropic.Message) reason.Completion {
	var comp reason.Completion
	comp.Message.Role = "assistant"

	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			comp.Message.Content += b.Text
		case anthropic.ToolUseBlock:
			comp.ToolCalls = append(comp.ToolCalls, reason.ToolCall{
				ID:   b.ID,
				Name: b.Name,
				Args: json.RawMessage(b.JSON.Input.Raw()),
			})
		}
	}
	comp.Usage = reason.Usage{
		InputTokens:              int(resp.Usage.InputTokens),
		CacheCreationInputTokens: int(resp.Usage.CacheCreationInputTokens),
		CacheReadInputTokens:     int(resp.Usage.CacheReadInputTokens),
	}
	return comp
}
