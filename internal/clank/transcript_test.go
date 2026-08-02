package clank_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/clank"
	"github.com/ianeff/thump/internal/reason"
)

// TestTurn_RoundTripsCallsPairedWithTheirResults pins the audit artifact, not
// the wire: a checkpointed Turn is what debug-transcript reads and what a
// post-incident review reconstructs the run from, so a call that loses its
// answer on the way through JSON is an unreadable transcript, not just a
// serialization bug.
func TestTurn_RoundTripsCallsPairedWithTheirResults(t *testing.T) {
	t.Parallel()
	want := clank.Turn{
		RunID: "slo_burn:ceph-rgw/1785097470705542441",
		Step:  2,
		Msgs: []reason.Message{
			{Role: "user", Content: "signal on ceph-rgw"},
			{Role: "assistant", Content: "checking burn", ToolCalls: []reason.ToolCall{
				{ID: "toolu_a", Name: "metrics", Args: json.RawMessage(`{"q":"burn"}`)},
				{ID: "toolu_b", Name: "metrics", Args: json.RawMessage(`{"q":"errors"}`)},
			}},
			{Role: "tool", ToolResults: []reason.ToolResult{
				{CallID: "toolu_a", Digest: "burn = 4 [cite: burn]"},
				{CallID: "toolu_b", Digest: "errors = 0 [cite: errors]"},
			}},
		},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got clank.Turn
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("a checkpointed turn must survive the transcript intact (-want +got)\n", diff)
	}
}

// TestTurn_DecodesATranscriptWrittenBeforeCallsWereCarried is the
// backward-compatibility claim the evidence/ archive depends on: adding fields
// to Message must not orphan a transcript already on disk.
func TestTurn_DecodesATranscriptWrittenBeforeCallsWereCarried(t *testing.T) {
	t.Parallel()
	archived := `{"RunID":"fp/1","Step":0,"Msgs":[{"Role":"assistant","Content":"checking"}]}`

	var got clank.Turn
	if err := json.Unmarshal([]byte(archived), &got); err != nil {
		t.Fatal(err)
	}

	want := clank.Turn{RunID: "fp/1", Step: 0, Msgs: []reason.Message{{Role: "assistant", Content: "checking"}}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error("an archived transcript must still decode (-want +got)\n", diff)
	}
}
