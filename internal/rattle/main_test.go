package rattle_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/rattle"
)

// startWithUnreadableWatch drives Main past config loading and into LoadWatch
// with a watch path that does not exist, returning the exit code and whatever
// the startup path logged. Every other required var is satisfied so the failure
// under test is the only one in play.
func startWithUnreadableWatch(t *testing.T) (code int, stdout string) {
	t.Helper()
	t.Setenv("PROM_URL", "http://prometheus.invalid:9090")
	t.Setenv("RATTLE_WATCH", filepath.Join(t.TempDir(), "no-such-watch.yaml"))
	t.Setenv("RATTLE_QUERY_CONFIG", "../../config/thump-test/rattle/query.yaml")

	var out, errb bytes.Buffer
	code = rattle.Main(nil, &out, &errb, "test", "none", "unknown")
	return code, out.String()
}

// TestMain_ReportsAMissingWatchListAndExitsNonZero pins the sharper half of the
// startup contract: not just that a bad config exits 1, but that the operator
// is told which file the beat could not read. A stranger's first hour is spent
// on exactly this failure.
func TestMain_ReportsAMissingWatchListAndExitsNonZero(t *testing.T) {
	code, stdout := startWithUnreadableWatch(t)

	if want := 1; want != code {
		t.Errorf("wrong exit code for an unreadable watch list: want %d, got %d", want, code)
	}
	if !strings.Contains(stdout, "no-such-watch.yaml") {
		t.Error("startup failure did not name the file it could not read", stdout)
	}
}

// TestMain_ReportsStartupFailureAsAStructuredRecordNotBareText holds startup
// failures to the same JSON handler the rest of the process logs through — a
// beat that dies before its first log line still has to be readable by whatever
// is collecting the others.
func TestMain_ReportsStartupFailureAsAStructuredRecordNotBareText(t *testing.T) {
	_, stdout := startWithUnreadableWatch(t)

	// beat.Start already logged "starting rattle", so the buffer holds two
	// concatenated objects — the failure is the last line, not the whole thing.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatal("startup failure is not a structured record", err)
	}
	if want := "ERROR"; want != rec["level"] {
		t.Errorf("wrong level on a startup failure: want %q, got %v", want, rec["level"])
	}
	if want := "rattle"; want != rec["beat"] {
		t.Errorf("wrong beat on a startup failure: want %q, got %v", want, rec["beat"])
	}
	if want := "load watch list"; want != rec["msg"] {
		t.Errorf("wrong msg on a startup failure: want %q, got %v", want, rec["msg"])
	}
}
