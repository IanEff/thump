package thump_test

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
// helper must NOT call t.Parallel() — same constraint internal/clank's copy
// of this helper documents.
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

// onlyOutcomeLine asserts exactly one captured line is the "outcome" line
// and returns it.
func onlyOutcomeLine(t *testing.T, lines []map[string]any) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range lines {
		if l["msg"] == "outcome" {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %q log line, got %d: %+v", "outcome", len(found), lines)
	}
	return found[0]
}
