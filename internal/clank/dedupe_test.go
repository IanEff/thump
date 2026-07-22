package clank_test

import (
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/api/v1/proposal"
	"github.com/ianeff/thump/internal/clank"
)

func TestPropose_ReleasesDedupeOnceTheWindowElapses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		sig := sigBurnAccel()
		round := func() *fakeModel {
			return &fakeModel{script: []clank.Completion{
				{ToolCalls: []clank.ToolCall{{Name: "metrics", Args: json.RawMessage(`{"q":"x"}`)}}},
				{ToolCalls: []clank.ToolCall{{Name: "propose", Args: proposeArgs(t, proposal.Set{
					FailureClass: proposal.ClassDependencySaturation,
					Proposals:    []proposal.Candidate{{ID: "p1", ContractRef: "throttle-non-critical-paths", Confidence: 0.89, Citations: []string{`{"q":"x"}`}}},
				})}}},
			}}
		}
		e, _ := newTestEngine(round())
		e.DedupeWindow = 50 * time.Millisecond

		first, err := e.Propose(ctx, sig)
		if err != nil {
			t.Fatalf("first Propose errored: %v", err)
		}
		if !first.Gate.DedupeOK {
			t.Fatalf("a clean fingerprint must clear dedupe: %+v", first.Gate)
		}

		e.Model = round() // fresh script — the first is exhausted
		suppressed, err := e.Propose(ctx, sig)
		if err != nil {
			t.Fatalf("second Propose errored: %v", err)
		}
		if suppressed.Gate.DedupeOK {
			t.Error("a set held open inside the window must suppress a redelivery")
		}

		time.Sleep(e.DedupeWindow + time.Millisecond)

		e.Model = round()
		released, err := e.Propose(ctx, sig)
		if err != nil {
			t.Fatalf("third Propose errored: %v", err)
		}
		if !released.Gate.DedupeOK {
			t.Error("once the window elapses, a still-open (held) set must release — a fresh proposal (re-notify) clears dedupe")
		}
	})
}
