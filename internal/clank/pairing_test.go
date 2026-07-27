package clank_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
)

// TestPropose_PairsEveryToolResultWithTheCallThatAskedForIt pins a failure that
// never surfaces as an error: with two calls in one turn and no pairing, the
// model receives two anonymous digests and cannot tell which answered which. It
// shows up as bad reasoning, never as a red build.
func TestPropose_PairsEveryToolResultWithTheCallThatAskedForIt(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{
			{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)},
		}},
		{ToolCalls: []clank.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"stub"}`)}}},
	}}

	e, _ := newTestEngine(model)
	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	// received[1] is the history as replayed on the SECOND call — the first
	// turn's calls and their answers are in it, or they were never sent.
	var calls []clank.ToolCall
	var results []clank.ToolResult
	for _, msg := range model.received[1] {
		calls = append(calls, msg.ToolCalls...)
		results = append(results, msg.ToolResults...)
	}

	if diff := cmp.Diff(2, len(calls)); diff != "" {
		t.Error("the assistant turn must replay the calls it made (-want +got)\n", diff)
	}
	if diff := cmp.Diff(2, len(results)); diff != "" {
		t.Error("both tool results must reach the model (-want +got)\n", diff)
	}

	asked := make(map[string]bool, len(calls))
	for _, c := range calls {
		if c.ID == "" {
			t.Error("a call with no ID cannot be answered — the pair has no key")
			continue
		}
		asked[c.ID] = true
	}
	for _, r := range results {
		if !asked[r.CallID] {
			t.Errorf("result %q answers no call this turn made — it is an anonymous digest", r.CallID)
		}
	}
}

// TestPropose_ReturnsAParallelTurnsResultsInASingleMessage pins the documented
// wire rule: splitting one turn's results across messages trains the model to
// stop making parallel calls, and every live turn measured so far issued two to
// five at once.
func TestPropose_ReturnsAParallelTurnsResultsInASingleMessage(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{
			{Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
			{Name: "metrics", Args: json.RawMessage(`{"q":"latency_p99"}`)},
		}},
		{ToolCalls: []clank.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"stub"}`)}}},
	}}

	e, _ := newTestEngine(model)
	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	var toolTurns int
	for _, msg := range model.received[1] {
		if msg.Role == "tool" {
			toolTurns++
		}
	}
	if diff := cmp.Diff(1, toolTurns); diff != "" {
		t.Error("one turn's results must ride in one message (-want +got)\n", diff)
	}
}

// TestPropose_TellsTheModelAboutAToolItDoesNotHave pins that an unknown tool is
// answered rather than dropped: an unanswered call reads to the model as a tool
// that returned nothing, and on the wire it is an unpaired block.
func TestPropose_TellsTheModelAboutAToolItDoesNotHave(t *testing.T) {
	t.Parallel()
	model := &fakeModel{script: []clank.Completion{
		{ToolCalls: []clank.ToolCall{{Name: "tea", Args: json.RawMessage(`{}`)}}},
		{ToolCalls: []clank.ToolCall{{Name: "insufficient", Args: json.RawMessage(`{"reason":"stub"}`)}}},
	}}

	e, _ := newTestEngine(model)
	if _, err := e.Propose(context.Background(), sigBurnAccel()); err != nil {
		t.Fatal(err)
	}

	var got []clank.ToolResult
	for _, msg := range model.received[1] {
		got = append(got, msg.ToolResults...)
	}
	if diff := cmp.Diff(1, len(got)); diff != "" {
		t.Error("an unknown tool still gets an answer (-want +got)\n", diff)
	}
	if len(got) == 1 && !got[0].IsError {
		t.Error("an unknown tool's answer must be marked an error, not a normal digest")
	}
}
