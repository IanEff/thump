package thump_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/thump"
)

func lastJSONRecord(t *testing.T, stdout string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatalf("no stdout output, want structured JSON records: %q", stdout)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("last line is not valid JSON: %v\nraw: %s", err, lines[len(lines)-1])
	}
	return rec
}

func TestMain_PrintsVersionAndReturnsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := thump.Main([]string{"-version"}, &out, &errb, "1.2.3", "abc123", "2026-07-02", nil, nil)
	if code != 0 {
		t.Errorf("version should exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "thump 1.2.3") {
		t.Error("version output missing the version:", out.String())
	}
}

func TestMain_MissingInboxReturnsOne(t *testing.T) {
	t.Setenv("THUMP_INBOX", "") // hermetic — don't inherit the shell's
	var out, errb bytes.Buffer
	code := thump.Main(nil, &out, &errb, "dev", "none", "unknown", nil, nil)
	if code != 1 {
		t.Errorf("missing THUMP_INBOX should exit 1, got %d", code)
	}
	rec := lastJSONRecord(t, out.String())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["beat"] != "thump" {
		t.Errorf("beat = %v, want thump", rec["beat"])
	}
	if rec["msg"] != "load config" {
		t.Errorf("msg = %v, want 'load config'", rec["msg"])
	}
	if errVal, _ := rec["err"].(string); !strings.Contains(errVal, "THUMP_INBOX") {
		t.Errorf("err attribute should name the missing var, got %q", errVal)
	}
}

func TestMain_MissingOutboxReturnsOne(t *testing.T) {
	t.Setenv("THUMP_INBOX", t.TempDir())
	t.Setenv("THUMP_OUTBOX", "")
	var out, errb bytes.Buffer
	code := thump.Main(nil, &out, &errb, "dev", "none", "unknown", nil, nil)
	if code != 1 {
		t.Errorf("missing THUMP_OUTBOX should exit 1, got %d", code)
	}
	rec := lastJSONRecord(t, out.String())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["beat"] != "thump" {
		t.Errorf("beat = %v, want thump", rec["beat"])
	}
	if rec["msg"] != "load config" {
		t.Errorf("msg = %v, want 'load config'", rec["msg"])
	}
	if errVal, _ := rec["err"].(string); !strings.Contains(errVal, "THUMP_OUTBOX") {
		t.Errorf("err attribute should name the missing var, got %q", errVal)
	}
}

func TestMain_OnceRunsASingleTickAndReturnsZeroInsteadOfLoopingForever(t *testing.T) {
	// A real shipped catalog, not a fixture — an empty inbox never resolves
	// a ContractRef against it, so any well-formed catalog file proves the
	// same thing: --once reaches a real Tick and returns, rather than
	// blocking in poll.Loop forever the way the flag's absence would.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("THUMP_INBOX", t.TempDir())
	t.Setenv("THUMP_OUTBOX", t.TempDir())
	t.Setenv("ACTION_CATALOG", filepath.Join(repoRoot, "config", "dev", "actions", "catalog.yaml"))

	var out, errb bytes.Buffer
	code := thump.Main([]string{"-once"}, &out, &errb, "dev", "none", "unknown", nil, nil)
	if code != 0 {
		t.Fatalf("want exit 0 for a single successful pass over an empty inbox, got %d, stderr: %s", code, errb.String())
	}
}

func TestMain_ReturnsNonZeroWhenRequiredConfigIsMissing(t *testing.T) {
	t.Setenv("ACTION_CATALOG", "")

	var stdout, stderr bytes.Buffer
	code := thump.Main(nil, &stdout, &stderr, "dev", "none", "unknown", nil, nil)

	if code != 1 {
		t.Errorf("want exit code 1 for missing config, got %d", code)
	}
	rec := lastJSONRecord(t, stdout.String())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["beat"] != "thump" {
		t.Errorf("beat = %v, want thump", rec["beat"])
	}
	if rec["msg"] != "load config" {
		t.Errorf("msg = %v, want 'load config'", rec["msg"])
	}
}
