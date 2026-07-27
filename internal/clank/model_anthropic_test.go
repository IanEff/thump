package clank

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-cmp/cmp"
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
		msgs []Message
		want []string // block types, in order, flattened across messages
	}{
		"a tool-only assistant turn sends a tool_use block and no empty text": {
			msgs: []Message{{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "t1", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			}}},
			want: []string{"tool_use"},
		},
		"a narrated tool turn sends text and tool_use together, in that order": {
			msgs: []Message{{Role: "assistant", Content: "checking burn", ToolCalls: []ToolCall{
				{ID: "t1", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			}}},
			want: []string{"text", "tool_use"},
		},
		"one turn's parallel results ride in one message": {
			msgs: []Message{{Role: "tool", ToolResults: []ToolResult{
				{CallID: "t1", Digest: "burn = 4"},
				{CallID: "t2", Digest: "errors = 0"},
			}}},
			want: []string{"tool_result", "tool_result"},
		},
		"a message carrying nothing at all is dropped rather than sent empty": {
			msgs: []Message{{Role: "assistant"}},
			want: nil,
		},
		"the seed prompt still sends one text block": {
			msgs: []Message{{Role: "user", Content: "signal on ceph-rgw"}},
			want: []string{"text"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, blockTypes(toAnthropicMessageParams(tc.msgs))); diff != "" {
				t.Error("wrong blocks on the wire (-want +got)\n", diff)
			}
		})
	}
}

func TestToAnthropicMessageParams_RolesTheTurnsTheWayTheAPIReadsThem(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		msg  Message
		want anthropic.MessageParamRole
	}{
		"an assistant turn is sent as the assistant": {
			msg:  Message{Role: "assistant", Content: "checking"},
			want: anthropic.MessageParamRoleAssistant,
		},
		"a tool turn is sent as the user, because tool_result is a user block": {
			msg:  Message{Role: "tool", ToolResults: []ToolResult{{CallID: "t1", Digest: "burn = 4"}}},
			want: anthropic.MessageParamRoleUser,
		},
		"the seed prompt is sent as the user": {
			msg:  Message{Role: "user", Content: "signal on ceph-rgw"},
			want: anthropic.MessageParamRoleUser,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := toAnthropicMessageParams([]Message{tc.msg})
			if len(got) != 1 {
				t.Fatalf("want exactly one rendered message, got %d", len(got))
			}
			if diff := cmp.Diff(tc.want, got[0].Role); diff != "" {
				t.Error("wrong role on the wire (-want +got)\n", diff)
			}
		})
	}
}
