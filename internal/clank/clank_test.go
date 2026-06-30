package clank_test

import (
	"bytes"
	"testing"

	"github.com/google/go-cmp/cmp"
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
	want := "clank v1.0.0\ncommit: abcdef\nbuilt: 2026-06-29\n"
	if diff := cmp.Diff(want, stdout.String()); diff != "" {
		t.Error("wrong --version output (-want +got)\n", diff)
	}
}
