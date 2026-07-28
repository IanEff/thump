package hiss_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/beat"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/publish"
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
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(policyPath, []byte("version: v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HISS_POLICY", policyPath) // valid, so the inbox check is what's actually under test
	t.Setenv("HISS_INBOX", "")          // hermetic — don't inherit the shell's
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

func TestMain_ReturnsNonZeroWhenRequiredConfigIsMissing(t *testing.T) {
	t.Setenv("HISS_POLICY", "")

	var stdout, stderr bytes.Buffer
	code := hiss.Main(nil, &stdout, &stderr, "dev", "none", "unknown")

	if code != 1 {
		t.Errorf("want exit code 1 for missing config, got %d", code)
	}
	if stderr.Len() == 0 {
		t.Error("want error message printed to stderr, got none")
	}
}

func TestTransport_OfflinePollExecutesTickAndExitsOnContextCancel(t *testing.T) {
	t.Parallel()
	inbox := t.TempDir()
	outbox := t.TempDir()
	policyFile := filepath.Join(t.TempDir(), "policy.yaml")

	if err := os.WriteFile(policyFile, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pol, err := hiss.LoadPolicy(policyFile)
	if err != nil {
		t.Fatal(err)
	}

	synctest.Test(t, func(t *testing.T) {
		tr := &hiss.Transport{
			Inbox:  inbox,
			Pub:    &publish.DirPublisher[decision.Governed]{Dir: outbox, Name: func(g decision.Governed) string { return g.Decision.SignalRef }},
			Policy: pol,
			Log:    hiss.NewDecisionLog(),
		}

		ctx, cancel := context.WithCancel(context.Background())

		if err := tr.Tick(ctx); err != nil {
			t.Errorf("want nil error on empty inbox tick, got %v", err)
		}

		cancel()
		beat.PollLoop(ctx, beat.PollConfig{Interval: 5 * time.Second, Timeout: 20 * time.Second}, tr.Tick)
	})
}
