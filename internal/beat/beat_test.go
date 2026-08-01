package beat_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ianeff/thump/internal/beat"
)

func TestStart_VersionFlagPrintsAndExits(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lc, code, exit := beat.Start("hiss", []string{"--version"}, &stdout, &stderr,
		beat.Version{Version: "v1.2.3", Commit: "abc", Date: "2026-07-09"})

	if !exit || code != 0 {
		t.Fatalf("--version must exit 0: got exit=%v code=%d", exit, code)
	}
	if lc.Ctx != nil {
		t.Errorf("no lifecycle context on the exit path, got %v", lc.Ctx)
	}
	if got := stdout.String(); !strings.Contains(got, "hiss v1.2.3") || !strings.Contains(got, "commit: abc") {
		t.Errorf("version output missing name/stamps:\n%s", got)
	}
}

func TestStart_UnparseableFlagExitsNonZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, code, exit := beat.Start("hiss", []string{"--nope"}, &stdout, &stderr, beat.Version{})

	if !exit || code != 1 {
		t.Fatalf("a bad flag must exit 1: got exit=%v code=%d", exit, code)
	}
	if !strings.Contains(stderr.String(), "failed to parse flags") {
		t.Errorf("expected a parse-error message on stderr, got %q", stderr.String())
	}
}

func TestStart_RunningPathReadsNATSAndCancelsOnStop(t *testing.T) {
	t.Setenv("NATS_URL", "nats://example:4222")
	var stdout, stderr bytes.Buffer
	lc, code, exit := beat.Start("hiss", nil, &stdout, &stderr, beat.Version{})

	if exit || code != 0 {
		t.Fatalf("normal start must not exit: got exit=%v code=%d", exit, code)
	}
	if lc.NATSURL != "nats://example:4222" {
		t.Errorf("Lifecycle.NATSURL = %q, want the env value", lc.NATSURL)
	}
	if err := lc.Ctx.Err(); err != nil {
		t.Errorf("context must be live before Stop, got %v", err)
	}
	lc.Stop()
	if !errors.Is(lc.Ctx.Err(), context.Canceled) {
		t.Errorf("Stop must cancel the context, got %v", lc.Ctx.Err())
	}
}

func TestExitOnError(t *testing.T) {
	t.Parallel()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want int
	}{
		{"no error", context.Background(), nil, 0},
		{"error, live context", context.Background(), errors.New("boom"), 1},
		{"error, cancelled context is a clean shutdown", cancelled, errors.New("boom"), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := beat.ExitOnError(tc.ctx, tc.err); got != tc.want {
				t.Errorf("ExitOnError = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNewWALPublisher_RejectsEmptyWALDir(t *testing.T) {
	t.Parallel()
	_, _, err := beat.NewWALPublisher[int](nil, "", "hiss", "thump.decisions", beat.DefaultWALConfig())
	if err == nil {
		t.Fatal("an empty WAL_DIR must be rejected, got nil error")
	}
}

func TestPollLoop_ReturnsPromptlyOnCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the loop must return without ever ticking

	ticked := false
	done := make(chan struct{})
	go func() {
		beat.PollLoop(ctx, beat.PollConfig{Interval: time.Hour}, func(context.Context) error {
			ticked = true
			return nil
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PollLoop did not return on a cancelled context")
	}
	if ticked {
		t.Error("PollLoop ticked despite the context being cancelled")
	}
}

// TestExitOnError_TreatsALostBrokerAsAFailureNotACleanShutdown separates the
// two cancelled contexts that otherwise look identical: a SIGTERM exits 0, a
// broker that went away for good exits 1 so the orchestrator restarts the pod.
func TestExitOnError_TreatsALostBrokerAsAFailureNotACleanShutdown(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		cause error
		want  int
	}{
		"ExitOnError returns 0 when an operator's shutdown cancelled the run":   {cause: nil, want: 0},
		"ExitOnError returns 1 when a lost broker connection cancelled the run": {cause: beat.ErrBrokerClosed, want: 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(tc.cause)

			got := beat.ExitOnError(ctx, context.Canceled)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Error("wrong exit code for a cancelled run", diff)
			}
		})
	}
}

func TestStart_HelpFlagPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	lc, code, exit := beat.Start("hiss", []string{"--help"}, &stdout, &stderr, beat.Version{Version: "v1.0.0"})

	if !exit {
		t.Error("want exit=true for --help flag; got false")
	}
	if code != 0 {
		t.Errorf("want code=0 for --help flag; got %d", code)
	}
	if lc.Ctx != nil {
		t.Errorf("want nil context on help exit path; got %v", lc.Ctx)
	}
	if stderr.Len() == 0 {
		t.Error("want usage text written to stderr, got empty output")
	}
}
