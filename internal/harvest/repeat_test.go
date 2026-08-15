package harvest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/internal/harvest"
)

func tworowTable() harvest.Table {
	base := harvest.Scenario{
		SignalRef:    testSignalRef,
		Fault:        harvest.Action{Path: "noop", Apply: "exec"},
		Restore:      harvest.Action{Path: "noop", Apply: "exec"},
		Expects:      harvest.Expects{Verdict: "approved"},
		SettleWindow: time.Minute,
	}
	first, second := base, base
	first.Name = "row-a"
	second.Name = "row-b"
	return harvest.Table{Scenarios: []harvest.Scenario{first, second}}
}

// TestRun_RepeatsRoundRobinAcrossRows pins the interleaving Lane D promises:
// N passes cycle through every row once each, rather than repeating one row N
// times before moving to the next — round-robin spreads any rig drift evenly
// instead of concentrating it in whichever row ran last.
func TestRun_RepeatsRoundRobinAcrossRows(t *testing.T) {
	t.Parallel()
	table := tworowTable()
	legs := harvest.Legs{Outcomes: feedWatcher{outcome.ResultSuccess}, Sets: feedSetWatcher(nil)}
	h := harvest.NewHarvest(legs, &recordingRunner{}, 0)

	var stdout, stderr bytes.Buffer
	code := harvest.RunForTest(t.Context(), h, table, "", true, 3, 0, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("want exit code 0, got %d (stderr: %s)", code, stderr.String())
	}

	type line struct {
		ScenarioName string `json:"scenarioName"`
		RunIndex     int    `json:"runIndex"`
	}
	var got []line
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var l line
		if err := dec.Decode(&l); err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}

	want := []line{
		{ScenarioName: "row-a", RunIndex: 0},
		{ScenarioName: "row-b", RunIndex: 0},
		{ScenarioName: "row-a", RunIndex: 1},
		{ScenarioName: "row-b", RunIndex: 1},
		{ScenarioName: "row-a", RunIndex: 2},
		{ScenarioName: "row-b", RunIndex: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d results, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("result %d: want %+v, got %+v", i, want[i], got[i])
		}
	}
}

// TestRun_ACancelledCtxStopsDuringCooldownWithoutStartingTheNextRow pins the
// other half of Lane D: the cooldown sleep selects on ctx.Done(), so an
// operator's Ctrl-C between two rows ends the loop immediately rather than
// waiting out the rest of a multi-minute cooldown before noticing.
func TestRun_ACancelledCtxStopsDuringCooldownWithoutStartingTheNextRow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		table := tworowTable()
		legs := harvest.Legs{Outcomes: feedWatcher{outcome.ResultSuccess}, Sets: feedSetWatcher(nil)}
		runner := &recordingRunner{}
		h := harvest.NewHarvest(legs, runner, 0)

		ctx, cancel := context.WithCancel(t.Context())
		var stdout, stderr bytes.Buffer
		done := make(chan int)
		go func() {
			done <- harvest.RunForTest(ctx, h, table, "", true, 1, time.Hour, &stdout, &stderr)
		}()

		// Let the first row run to completion and settle into its cooldown
		// sleep before cancelling — synctest advances fake time until every
		// goroutine is blocked, so this happens the instant row-a's Run
		// returns, not after any real wall-clock delay.
		synctest.Wait()
		cancel()

		code := <-done
		if code != 0 {
			t.Errorf("want exit code 0 on a clean cancel, got %d", code)
		}

		calls := runner.snapshot()
		if !strings.Contains(strings.Join(calls, "\n"), "noop") {
			t.Error("row-a's restore never ran", calls)
		}
		if strings.Count(stdout.String(), `"scenarioName":"row-b"`) != 0 {
			t.Error("row-b started despite the cancel landing during row-a's cooldown", stdout.String())
		}
	})
}
