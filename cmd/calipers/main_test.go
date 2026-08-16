package main_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// binPath is the calipers binary built once by TestMain — every subtest
// execs the real binary rather than calling calipers.Main directly, because
// the bug class this file exists to catch (os.Args misindexed, stdout and
// stderr swapped, the exit code dropped instead of returned to os.Exit)
// only shows up in the composition root, main.go, which cannot be driven
// in-process without ending the test process at the first os.Exit call.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "calipers-bin")
	if err != nil {
		panic(err)
	}

	binPath = filepath.Join(dir, "calipers")
	//nolint:gosec // G204: "go" and its flags are literals; only the freshly-chosen output path varies
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		panic("build calipers for testing: " + err.Error() + "\n" + string(out))
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// run execs the built calipers binary and reports what a caller of the real
// process sees: the two output streams kept separate, and the exit code
// pulled off the process rather than an error value.
func run(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	//nolint:gosec // G204: binPath is the binary this test built in TestMain, not user input
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("run calipers %v: %v", args, err)
		}
		exitCode = exitErr.ExitCode()
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// wantTopUsage mirrors calipers.wantTopUsage — it cannot be imported (this
// test drives the compiled binary as a subprocess, not the package) so it
// is pinned here too; a change to calipers.go's topUsage without a matching
// change here fails this test rather than passing silently.
const wantTopUsage = "usage: calipers <incidents|approve|force|unseal|corpus|rca|tune|replay|harvest|probe|transcript|scorecard> [flags]\n"

func TestMain_ReturnsUsageAndExitCodeTwoForBadInvocations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	tests := map[string]struct {
		args []string
	}{
		"no arguments at all prints usage and exits 2":  {args: nil},
		"an undocumented verb prints usage and exits 2": {args: []string{"polish"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, exitCode := run(t, dir, tc.args...)

			if diff := cmp.Diff(2, exitCode); diff != "" {
				t.Error("wrong exit code", diff)
			}
			if diff := cmp.Diff(wantTopUsage, stderr); diff != "" {
				t.Error("wrong usage line on stderr", diff)
			}
			if diff := cmp.Diff("", stdout); diff != "" {
				t.Error("usage error wrote to stdout, want stderr only", diff)
			}
		})
	}
}

// TestMain_RoutesArgsAndStreamsCorrectlyOnASuccessfulVerb catches exactly
// what a subprocess test is for and a direct call to calipers.Main is not:
// main.go passing os.Args instead of os.Args[1:] would route on the binary
// path and fall through to the usage branch, and stdout/stderr swapped
// would move this incident listing onto stderr. Either mistake fails this
// test; neither is visible to a test that calls calipers.Main directly.
func TestMain_RoutesArgsAndStreamsCorrectlyOnASuccessfulVerb(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // empty inbox: incidents has nothing to fold, so the happy path is exit 0 with a blank listing

	stdout, stderr, exitCode := run(t, dir, "incidents")

	if diff := cmp.Diff(0, exitCode); diff != "" {
		t.Error("wrong exit code", diff)
	}
	if diff := cmp.Diff("", stderr); diff != "" {
		t.Error("wrong stderr", diff)
	}
	if diff := cmp.Diff("\n", stdout); diff != "" {
		t.Error("wrong stdout", diff)
	}
}
