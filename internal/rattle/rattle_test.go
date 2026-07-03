package rattle_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
)

func TestMain_PrintsVersionAndReturnsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := rattle.Main([]string{"-version"}, &out, &errb, "1.2.3", "abc123", "2026-07-01")
	if code != 0 {
		t.Errorf("version should exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "rattle 1.2.3") {
		t.Error("version output mmissing the version", cmp.Diff("rattle 1.2.3", out.String()))
	}
}

func TestMain_MissingPromURLReturnsOne(t *testing.T) {
	t.Setenv("PROM_URL", "") // hermetic — don't inherit the shell's
	var out, errb bytes.Buffer
	code := rattle.Main(nil, &out, &errb, "dev", "none", "unknown")
	if code != 1 {
		t.Errorf("missing PROM_URL should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "PROM_URL") {
		t.Error("stderr should name the missing var", errb.String())
	}
}
