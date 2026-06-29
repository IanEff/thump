package clank_test

import (
	"bytes"
	"testing"

	"github.com/ianeff/clank/internal/clank"
)

func TestMain_VersionFlag(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	args := []string{"-version"}
	exitCode := clank.Main(args, stdout, stderr, "v1.0.0", "abcdef", "2026-06-29")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	expectedOut := "clank v1.0.0\ncommit: abcdef\nbuilt: 2026-06-29\n"
	if stdout.String() != expectedOut {
		t.Errorf("expected output %q, got %q", expectedOut, stdout.String())
	}
}
