package hiss_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ianeff/thump/internal/hiss"
)

func TestMain_PrintsVersionAndReturnsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := hiss.Main([]string{"-version"}, &out, &errb, "1.2.3", "abc123", "2026-07-01")
	if code != 0 {
		t.Errorf("version should exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "hiss 1.2.3") {
		t.Error("version output missing the version:", out.String())
	}
}

func TestMain_MissingInboxReturnsOne(t *testing.T) {
	t.Setenv("HISS_INBOX", "") // hermetic — don't inherit the shell's
	var out, errb bytes.Buffer
	code := hiss.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing HISS_INBOX should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "HISS_INBOX") {
		t.Error("stderr should name the missing var:", errb.String())
	}
}

func TestMain_UnreadablePolicyReturnsOne(t *testing.T) {
	t.Setenv("HISS_INBOX", t.TempDir())
	t.Setenv("HISS_OUTBOX", t.TempDir())
	t.Setenv("HISS_POLICY", filepath.Join(t.TempDir(), "no-such-policy.yaml"))
	var out, errb bytes.Buffer
	code := hiss.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("an unreadable policy file should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "policy") {
		t.Error("stderr should say the policy failed to load:", errb.String())
	}
}
