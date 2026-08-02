package clank

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/reason"
	"google.golang.org/genai"
)

// partKinds flattens the rendered contents into the part sequence the SDK
// sends, paired with each part's role.
func partKinds(contents []*genai.Content) []string {
	var got []string
	for _, c := range contents {
		for _, p := range c.Parts {
			switch {
			case p.Text != "":
				got = append(got, string(c.Role)+":text")
			case p.FunctionCall != nil:
				got = append(got, string(c.Role)+":function_call")
			case p.FunctionResponse != nil:
				got = append(got, string(c.Role)+":function_response")
			default:
				got = append(got, string(c.Role)+":unknown")
			}
		}
	}
	return got
}

func TestToGeminiContents_BuildsThePartsTheAPIExpects(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		msgs []reason.Message
		want []string
	}{
		"a tool-only model turn sends a function_call part and no empty text": {
			msgs: []reason.Message{{Role: "assistant", ToolCalls: []reason.ToolCall{
				{ID: "t1", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			}}},
			want: []string{"model:function_call"},
		},
		"one turn's parallel results ride in one content": {
			msgs: []reason.Message{{Role: "tool", ToolResults: []reason.ToolResult{
				{CallID: "t1", Name: "metrics", Digest: "burn = 4"},
				{CallID: "t2", Name: "metrics", Digest: "errors = 0"},
			}}},
			want: []string{"user:function_response", "user:function_response"},
		},
		"a message carrying nothing at all is dropped rather than sent empty": {
			msgs: []reason.Message{{Role: "assistant"}},
			want: nil,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, partKinds(toGeminiContents(tc.msgs))); diff != "" {
				t.Error("wrong parts on the wire (-want +got)\n", diff)
			}
		})
	}
}

// TestToGeminiContents_KeepsTheProvidersOwnCallIdentifier pins the pairing key:
// a function response is matched to its call by id, so dropping the id turns a
// parallel turn's answers back into anonymous digests.
func TestToGeminiContents_KeepsTheProvidersOwnCallIdentifier(t *testing.T) {
	t.Parallel()
	got := toGeminiContents([]reason.Message{{Role: "tool", ToolResults: []reason.ToolResult{
		{CallID: "t1", Name: "metrics", Digest: "burn = 4"},
	}}})
	if len(got) != 1 || len(got[0].Parts) != 1 || got[0].Parts[0].FunctionResponse == nil {
		t.Fatalf("want exactly one function_response part, got %+v", got)
	}
	resp := got[0].Parts[0].FunctionResponse
	if diff := cmp.Diff("t1", resp.ID); diff != "" {
		t.Error("the response must name the call it answers (-want +got)\n", diff)
	}
	if diff := cmp.Diff("metrics", resp.Name); diff != "" {
		t.Error("the response must name its function — the SDK requires it (-want +got)\n", diff)
	}
}
