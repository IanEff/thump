package anthropic

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ianeff/thump/internal/reason"
)

// FromAnthropicForTest exposes fromAnthropicMessage to anthropic_test — the
// wire-to-Completion fold, independent of a live Messages.New call.
func FromAnthropicForTest(resp *anthropic.Message) reason.Completion {
	return fromAnthropicMessage(resp)
}

// ToAnthropicMessageParamsForTest exposes toAnthropicMessageParams to
// anthropic_test — the Message-to-wire render, independent of the SDK call.
func ToAnthropicMessageParamsForTest(msgs []reason.Message) []anthropic.MessageParam {
	return toAnthropicMessageParams(msgs)
}

// ToAnthropicToolParamsForTest exposes toAnthropicToolParams to
// anthropic_test — the ToolSpec-to-wire render, independent of the SDK call.
func ToAnthropicToolParamsForTest(tools []reason.ToolSpec) []anthropic.ToolUnionParam {
	return toAnthropicToolParams(tools)
}
