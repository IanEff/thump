package clank_test

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/clank"
)

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
		msgs []clank.Message
		want []string // block types, in order, flattened across messages
	}{
		"a tool-only assistant turn sends a tool_use block and no empty text": {
			msgs: []clank.Message{{Role: "assistant", ToolCalls: []clank.ToolCall{
				{ID: "t1", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			}}},
			want: []string{"tool_use"},
		},
		"a narrated tool turn sends text and tool_use together, in that order": {
			msgs: []clank.Message{{Role: "assistant", Content: "checking burn", ToolCalls: []clank.ToolCall{
				{ID: "t1", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			}}},
			want: []string{"text", "tool_use"},
		},
		"one turn's parallel results ride in one message": {
			msgs: []clank.Message{{Role: "tool", ToolResults: []clank.ToolResult{
				{CallID: "t1", Digest: "burn = 4"},
				{CallID: "t2", Digest: "errors = 0"},
			}}},
			want: []string{"tool_result", "tool_result"},
		},
		"a message carrying nothing at all is dropped rather than sent empty": {
			msgs: []clank.Message{{Role: "assistant"}},
			want: nil,
		},
		"the seed prompt still sends one text block": {
			msgs: []clank.Message{{Role: "user", Content: "signal on ceph-rgw"}},
			want: []string{"text"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, blockTypes(clank.ToAnthropicMessageParamsForTest(tc.msgs))); diff != "" {
				t.Error("wrong blocks on the wire (-want +got)\n", diff)
			}
		})
	}
}

func TestToAnthropicMessageParams_RolesTheTurnsTheWayTheAPIReadsThem(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		msg  clank.Message
		want anthropic.MessageParamRole
	}{
		"an assistant turn is sent as the assistant": {
			msg:  clank.Message{Role: "assistant", Content: "checking"},
			want: anthropic.MessageParamRoleAssistant,
		},
		"a tool turn is sent as the user, because tool_result is a user block": {
			msg:  clank.Message{Role: "tool", ToolResults: []clank.ToolResult{{CallID: "t1", Digest: "burn = 4"}}},
			want: anthropic.MessageParamRoleUser,
		},
		"the seed prompt is sent as the user": {
			msg:  clank.Message{Role: "user", Content: "signal on ceph-rgw"},
			want: anthropic.MessageParamRoleUser,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := clank.ToAnthropicMessageParamsForTest([]clank.Message{tc.msg})
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
		want  clank.Usage
	}{
		"a response with a cache read reports the read and input counts": {
			usage: anthropic.Usage{CacheReadInputTokens: 1800, InputTokens: 200},
			want:  clank.Usage{CacheReadInputTokens: 1800, InputTokens: 200},
		},
		"a response with no cache activity reports zeros, not omission": {
			usage: anthropic.Usage{InputTokens: 500},
			want:  clank.Usage{InputTokens: 500},
		},
		"a response that wrote a new cache entry reports the write count": {
			usage: anthropic.Usage{CacheCreationInputTokens: 3200, InputTokens: 3200},
			want:  clank.Usage{CacheCreationInputTokens: 3200, InputTokens: 3200},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resp := &anthropic.Message{Usage: tc.usage}
			got := clank.FromAnthropicForTest(resp)
			if diff := cmp.Diff(tc.want, got.Usage); diff != "" {
				t.Error("wrong usage mapped onto Completion (-want +got)\n", diff)
			}
		})
	}
}
