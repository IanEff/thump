package reason

import "encoding/json"

// Message is one turn of the conversation fed to Model.Complete. Content is
// prose; ToolCalls and ToolResults carry the structure a provider needs to
// pair a call with its answer — a result is always a one-line digest, never a
// raw payload, so there is no field a raw payload could travel in.
type Message struct {
	Role        string
	Content     string
	ToolCalls   []ToolCall   `json:"ToolCalls,omitempty"`
	ToolResults []ToolResult `json:"ToolResults,omitempty"`
}

// Completion is one Model.Complete response: the assistant's Message plus
// any tool calls it made in the same turn.
type Completion struct {
	Message   Message
	ToolCalls []ToolCall
	Usage     Usage
}

// ToolCall is one tool invocation the model requested — the tool's name and
// its raw JSON args, decoded by whichever engine branch dispatches that name.
// ID is the provider's own identifier for the call, and is echoed back on the
// matching ToolResult.
type ToolCall struct {
	ID   string `json:"ID,omitempty"`
	Name string
	Args json.RawMessage
}

// ToolResult is one digest answering one ToolCall. CallID pairs it to the
// call; IsError marks a tool that failed, which the model is told about
// rather than shielded from.
type ToolResult struct {
	CallID  string
	Digest  string
	Name    string `json:"Name,omitempty"` // the tool the call named; required by the Gemini wire format, ignored by Anthropic's
	IsError bool   `json:"IsError,omitempty"`
}

// Usage is the token accounting for one Model.Complete call.  Zero
// values mean no cache activity.
type Usage struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}
