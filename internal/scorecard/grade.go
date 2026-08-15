// Package scorecard grades a harvest run's own JSONL output against the
// row's own authored expectation. It invents no judgement of its own — the
// same posture harvest.Main's doc comment and tune's NotYet take toward
// their own inputs — it only counts, buckets, and prints.
package scorecard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ianeff/thump/api/v1/outcome"
	"github.com/ianeff/thump/api/v1/proposal"
)

// row mirrors harvest.Result's wire shape field for field.
type row struct {
	ScenarioName     string                `json:"scenarioName"`
	ExpectedClass    proposal.FailureClass `json:"expectedClass"`
	ExpectedContract string                `json:"expectedContract"`
	ExpectedVerdict  string                `json:"expectedVerdict"`
	ActualVerdict    string                `json:"actualVerdict"`
	ActualContract   string                `json:"actualContract"`
	ActualResult     outcome.Result        `json:"actualResult"`
	ObservedSeverity *float64              `json:"observedSeverity,omitempty"`
	RunID            string                `json:"runID,omitempty"`
	RunIndex         int                   `json:"runIndex"`
	StartedAt        time.Time             `json:"startedAt"`
	EndedAt          time.Time             `json:"endedAt"`
	Err              string                `json:"err,omitempty"`
}

func (r row) failed() bool {
	return r.Err != ""
}

// reason buckets a non-counting run by why it didn't count. The empty
// reason marks a hit.
type reason string

const (
	hit              reason = ""
	refusedWrongBeat reason = "refused at wrong beat"
	// noDetection covers everything ErrSettleTimeout can mean: harvest.Result
	// carries no field saying whether a detection or a Set was ever seen on
	// a row whose settle errored, so "nothing ever detected" and "detected,
	// proposed, but never resolved" are indistinguishable from the Result
	// alone and share this one bucket rather than a fabricated split.
	noDetection    reason = "no detection / settle timeout"
	wrongContract  reason = "wrong contract"
	wrongVerdict   reason = "wrong verdict"
	nonConverging  reason = "non-converging"
	restoreFailure reason = "restore failure"
	harnessError   reason = "harness error"
)

// grade assigns a reason to one row, grading strictly against that row's
// own ExpectedX fields — never a floor, band, or gate value, none of which
// this phase is permitted to touch.
func grade(r row) reason {
	if r.failed() {
		return failedReason(r.Err)
	}
	switch {
	case r.ActualVerdict == "refused" && r.ExpectedVerdict != "refused":
		return refusedWrongBeat
	case r.ExpectedVerdict == "refused" && r.ActualVerdict != "refused":
		return refusedWrongBeat
	case r.ActualVerdict != r.ExpectedVerdict:
		return wrongVerdict
	case r.ExpectedVerdict == "refused":
		// Both sides agreed refused, and a refused row carries no contract
		// to compare — the match above already settled it.
		return hit
	case r.ActualContract != r.ExpectedContract:
		return wrongContract
	case r.ExpectedVerdict == "approved" && r.ActualResult != outcome.ResultSuccess:
		return nonConverging
	default:
		return hit
	}
}

// failedReason classifies a Result whose Err field was set. It reads the
// rendered message rather than the original error chain — harvest.Result.Err
// is text on the wire, not an error a caller can errors.Is against — but
// harvest.go's own wrapping puts a stable substring in every case this
// package cares about: ErrSettleTimeout's text for a row that never
// detected anything, and "restore failed"/"restore also failed" for the
// restore leg specifically.
func failedReason(msg string) reason {
	switch {
	case strings.Contains(msg, "restore"):
		return restoreFailure
	case strings.Contains(msg, "settle window elapsed"):
		return noDetection
	default:
		return harnessError
	}
}

// Report is a graded harvest run: n and rate at the top, then the same
// grade broken down by scenario and by RunIndex (the warming-trend confound
// named in the phase's own plan), then a histogram of why every
// non-counting row didn't count, then every non-counting row's RunID so a
// human can pull its transcript.
type Report struct {
	N          int
	Hits       int
	ByScenario map[string]Tally
	ByRunIndex map[int]Tally
	Reasons    map[reason]int
	Misses     []Miss
}

// Tally is n-of-m, kept as counts rather than a pre-divided rate so a
// caller can re-aggregate without floating-point drift.
type Tally struct {
	Hits int
	N    int
}

// Miss names one non-counting row well enough to find its transcript:
// `task dev:transcript RUN=<id>` per harvest.Result's own RunID doc
// comment. RunID is empty for a row that never got as far as a proposal —
// a refused-at-wrong-beat or no-detection miss has nothing to look up.
type Miss struct {
	ScenarioName string
	RunIndex     int
	RunID        string
	Reason       string
}

// Grade reads one harvest.Result per line from r and grades every row.
// A blank line is skipped; anything else that fails to parse as a Result
// is a caller error, not a grading outcome, and stops the read.
func Grade(r io.Reader) (Report, error) {
	rpt := Report{
		ByScenario: map[string]Tally{},
		ByRunIndex: map[int]Tally{},
		Reasons:    map[reason]int{},
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rw row
		if err := json.Unmarshal(line, &rw); err != nil {
			return Report{}, fmt.Errorf("scorecard: decode result: %w", err)
		}

		rpt.N++
		g := grade(rw)

		st := rpt.ByScenario[rw.ScenarioName]
		st.N++
		ri := rpt.ByRunIndex[rw.RunIndex]
		ri.N++

		if g == hit {
			rpt.Hits++
			st.Hits++
			ri.Hits++
		} else {
			rpt.Reasons[g]++
			rpt.Misses = append(rpt.Misses, Miss{
				ScenarioName: rw.ScenarioName,
				RunIndex:     rw.RunIndex,
				RunID:        rw.RunID,
				Reason:       string(g),
			})
		}
		rpt.ByScenario[rw.ScenarioName] = st
		rpt.ByRunIndex[rw.RunIndex] = ri
	}
	if err := sc.Err(); err != nil {
		return Report{}, fmt.Errorf("scorecard: read results: %w", err)
	}
	return rpt, nil
}
