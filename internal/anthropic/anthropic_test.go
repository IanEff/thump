package anthropic_test

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-cmp/cmp"

	anthropicpkg "github.com/ianeff/thump/internal/anthropic"
	"github.com/ianeff/thump/internal/reason"
)

// blockCacheControl reports b's cache breakpoint, or nil if none was set.
// GetCacheControl returns a non-nil pointer once a block's union variant is
// populated at all — true of every block this package renders — so Type
// (only ever non-empty via anthropic.NewCacheControlEphemeralParam) is the
// field that actually distinguishes marked from unmarked.
func blockCacheControl(b anthropic.ContentBlockParamUnion) *anthropic.CacheControlEphemeralParam {
	cc := b.GetCacheControl()
	if cc == nil || cc.Type == "" {
		return nil
	}
	return cc
}

// cacheBreakpoints flattens params into the indices of blocks carrying a
// cache breakpoint, the same flattening blockTypes does for block type.
func cacheBreakpoints(params []anthropic.MessageParam) []int {
	var got []int
	i := 0
	for _, p := range params {
		for _, b := range p.Content {
			if blockCacheControl(b) != nil {
				got = append(got, i)
			}
			i++
		}
	}
	return got
}

// toolCacheBreakpoints flattens params into the indices of tools carrying a
// cache breakpoint.
func toolCacheBreakpoints(params []anthropic.ToolUnionParam) []int {
	var got []int
	for i, p := range params {
		if cc := p.GetCacheControl(); cc != nil && cc.Type != "" {
			got = append(got, i)
		}
	}
	return got
}

// blockTypes flattens the rendered params into the block-type sequence the API
// sees, which is the only thing these rows are claiming anything about.
func blockTypes(params []anthropic.MessageParam) []string {
	var got []string
	for _, p := range params {
		for _, b := range p.Content {
			switch {
			case b.OfText != nil:
				got = append(got, "text")
			case b.OfToolUse != nil:
				got = append(got, "tool_use")
			case b.OfToolResult != nil:
				got = append(got, "tool_result")
			default:
				got = append(got, "unknown")
			}
		}
	}
	return got
}

func TestToAnthropicMessageParams_BuildsTheBlocksTheAPIExpects(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		msgs []reason.Message
		want []string // block types, in order, flattened across messages
	}{
		"a tool-only assistant turn sends a tool_use block and no empty text": {
			msgs: []reason.Message{{Role: "assistant", ToolCalls: []reason.ToolCall{
				{ID: "t1", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			}}},
			want: []string{"tool_use"},
		},
		"a narrated tool turn sends text and tool_use together, in that order": {
			msgs: []reason.Message{{Role: "assistant", Content: "checking burn", ToolCalls: []reason.ToolCall{
				{ID: "t1", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			}}},
			want: []string{"text", "tool_use"},
		},
		"one turn's parallel results ride in one message": {
			msgs: []reason.Message{{Role: "tool", ToolResults: []reason.ToolResult{
				{CallID: "t1", Digest: "burn = 4"},
				{CallID: "t2", Digest: "errors = 0"},
			}}},
			want: []string{"tool_result", "tool_result"},
		},
		"a message carrying nothing at all is dropped rather than sent empty": {
			msgs: []reason.Message{{Role: "assistant"}},
			want: nil,
		},
		"the seed prompt still sends one text block": {
			msgs: []reason.Message{{Role: "user", Content: "signal on ceph-rgw"}},
			want: []string{"text"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, blockTypes(anthropicpkg.ToAnthropicMessageParamsForTest(tc.msgs))); diff != "" {
				t.Error("wrong blocks on the wire (-want +got)\n", diff)
			}
		})
	}
}

func TestToAnthropicMessageParams_RolesTheTurnsTheWayTheAPIReadsThem(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		msg  reason.Message
		want anthropic.MessageParamRole
	}{
		"an assistant turn is sent as the assistant": {
			msg:  reason.Message{Role: "assistant", Content: "checking"},
			want: anthropic.MessageParamRoleAssistant,
		},
		"a tool turn is sent as the user, because tool_result is a user block": {
			msg:  reason.Message{Role: "tool", ToolResults: []reason.ToolResult{{CallID: "t1", Digest: "burn = 4"}}},
			want: anthropic.MessageParamRoleUser,
		},
		"the seed prompt is sent as the user": {
			msg:  reason.Message{Role: "user", Content: "signal on ceph-rgw"},
			want: anthropic.MessageParamRoleUser,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := anthropicpkg.ToAnthropicMessageParamsForTest([]reason.Message{tc.msg})
			if len(got) != 1 {
				t.Fatalf("want exactly one rendered message, got %d", len(got))
			}
			if diff := cmp.Diff(tc.want, got[0].Role); diff != "" {
				t.Error("wrong role on the wire (-want +got)\n", diff)
			}
		})
	}
}

func TestFromAnthropicMessage_MapsUsageOntoCompletion(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		usage anthropic.Usage
		want  reason.Usage
	}{
		"a response with a cache read reports the read and input counts": {
			usage: anthropic.Usage{CacheReadInputTokens: 1800, InputTokens: 200},
			want:  reason.Usage{CacheReadInputTokens: 1800, InputTokens: 200},
		},
		"a response with no cache activity reports zeros, not omission": {
			usage: anthropic.Usage{InputTokens: 500},
			want:  reason.Usage{InputTokens: 500},
		},
		"a response that wrote a new cache entry reports the write count": {
			usage: anthropic.Usage{CacheCreationInputTokens: 3200, InputTokens: 3200},
			want:  reason.Usage{CacheCreationInputTokens: 3200, InputTokens: 3200},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resp := &anthropic.Message{Usage: tc.usage}
			got := anthropicpkg.FromAnthropicForTest(resp)
			if diff := cmp.Diff(tc.want, got.Usage); diff != "" {
				t.Error("wrong usage mapped onto Completion (-want +got)\n", diff)
			}
		})
	}
}

func TestToAnthropicMessageParams_MarksTheLastMessagesLastBlockCacheable(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		msgs []reason.Message
		want []int // flattened block indices carrying CacheControl
	}{
		"a single user turn marks its only block": {
			msgs: []reason.Message{{Role: "user", Content: "seed prompt"}},
			want: []int{0},
		},
		"a turn ending in tool_result marks the tool_result block, not an earlier text block": {
			msgs: []reason.Message{
				{Role: "user", Content: "seed"},
				{Role: "assistant", ToolCalls: []reason.ToolCall{{ID: "t1", Name: "kube"}}},
				{Role: "user", ToolResults: []reason.ToolResult{{CallID: "t1", Digest: "ok"}}},
			},
			want: []int{2},
		},
		"empty messages mark nothing": {
			msgs: nil,
			want: nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := cacheBreakpoints(anthropicpkg.ToAnthropicMessageParamsForTest(tc.msgs))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong blocks carry CacheControl (-want +got)\n", diff)
			}
		})
	}
}

func TestToAnthropicToolParams_MarksTheLastToolCacheable(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		tools []reason.ToolSpec
		want  []int
	}{
		"multiple tools mark only the last one": {
			tools: []reason.ToolSpec{{Name: "insufficient"}, {Name: "kube"}, {Name: "loki"}},
			want:  []int{2},
		},
		"no tools marks nothing": {
			tools: nil,
			want:  nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := toolCacheBreakpoints(anthropicpkg.ToAnthropicToolParamsForTest(tc.tools))
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong tools carry CacheControl (-want +got)\n", diff)
			}
		})
	}
}
