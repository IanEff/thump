package thump_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/thump"
)

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
	if !strings.Contains(errb.String(), "THUMP_INBOX") {
		t.Error("stderr should name the missing var:", errb.String())
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
	if !strings.Contains(errb.String(), "THUMP_OUTBOX") {
		t.Error("stderr should name the missing var:", errb.String())
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
	if stderr.Len() == 0 {
		t.Error("want error message printed to stderr, got none")
	}
}
