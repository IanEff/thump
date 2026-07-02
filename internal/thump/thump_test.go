package thump_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ianeff/clank/internal/thump"
)

func TestMain_PrintsVersionAndReturnsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := thump.Main([]string{"-version"}, &out, &errb, "1.2.3", "abc123", "2026-07-02")
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
	code := thump.Main(nil, &out, &errb, "dev", "none", "unknown")
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
	code := thump.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing THUMP_OUTBOX should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "THUMP_OUTBOX") {
		t.Error("stderr should name the missing var:", errb.String())
	}
}
