package clank_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

// captureLog redirects the process-wide default slog logger to a JSON
// handler backed by an in-memory buffer, mirroring production's own handler
// (internal/beat.Start wires slog.NewJSONHandler), and restores the previous
// default on cleanup. The returned func parses every captured line into a
// map so a test asserts on structured fields, not on substring-matched text.
//
// slog.SetDefault mutates process-global state, so any test using this
// helper must NOT call t.Parallel(): Go runs every non-parallel top-level
// test to completion, in the sequential dispatch pass, strictly before any
// t.Parallel() test resumes its post-Parallel() body — that ordering
// guarantee is what keeps this safe under `go test -race` alongside the
// rest of this package's parallel tests. Calling t.Parallel() in a test
// that also calls captureLog would let two tests race on which is "the"
// default logger and on the shared buffer.
func captureLog(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []map[string]any {
		var lines []map[string]any
		sc := bufio.NewScanner(bytes.NewReader(buf.Bytes()))
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Fatalf("captureLog: non-JSON log line %q: %v", sc.Text(), err)
			}
			lines = append(lines, m)
		}
		return lines
	}
}

// onlyReasonedLine asserts exactly one captured line is the "reasoned"
// terminal line and returns it — a Propose call must produce exactly one,
// never zero (the silent-exit-path bug) and never two (a duplicate-logging
// regression, e.g. a caller still emitting its own copy alongside
// Propose's own).
func onlyReasonedLine(t *testing.T, lines []map[string]any) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range lines {
		if l["msg"] == "reasoned" {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %q log line, got %d: %+v", "reasoned", len(found), lines)
	}
	return found[0]
}
