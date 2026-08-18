package hiss_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ianeff/thump/api/v1/decision"
	"github.com/ianeff/thump/internal/hiss"
	"github.com/ianeff/thump/internal/poll"
	"github.com/ianeff/thump/internal/publish"
	"sigs.k8s.io/yaml"
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
	rec := lastJSONRecord(t, out.String())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["beat"] != "hiss" {
		t.Errorf("beat = %v, want hiss", rec["beat"])
	}
	if rec["msg"] != "load config" {
		t.Errorf("msg = %v, want 'load config'", rec["msg"])
	}
	if errVal, _ := rec["err"].(string); !strings.Contains(errVal, "HISS_INBOX") {
		t.Errorf("err attribute should name the missing var, got %q", errVal)
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
	rec := lastJSONRecord(t, out.String())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["beat"] != "hiss" {
		t.Errorf("beat = %v, want hiss", rec["beat"])
	}
	if rec["msg"] != "load policy" {
		t.Errorf("msg = %v, want 'load policy'", rec["msg"])
	}
	if errVal, _ := rec["err"].(string); !strings.Contains(errVal, "no-such-policy.yaml") {
		t.Errorf("err attribute should name the unreadable file, got %q", errVal)
	}
}

func TestMain_ReturnsNonZeroWhenRequiredConfigIsMissing(t *testing.T) {
	t.Setenv("HISS_POLICY", "")

	var stdout, stderr bytes.Buffer
	code := hiss.Main(nil, &stdout, &stderr, "dev", "none", "unknown")

	if code != 1 {
		t.Errorf("want exit code 1 for missing config, got %d", code)
	}
	rec := lastJSONRecord(t, stdout.String())
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["beat"] != "hiss" {
		t.Errorf("beat = %v, want hiss", rec["beat"])
	}
	if rec["msg"] != "load config" {
		t.Errorf("msg = %v, want 'load config'", rec["msg"])
	}
}

func TestMain_OnceRunsASingleTickAndReturnsZero(t *testing.T) {
	inbox, outbox := t.TempDir(), t.TempDir()
	writeSetYAML(t, inbox, "ps-001.yaml", governedSet())

	policyRaw, err := yaml.Marshal(calmPolicy())
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(policyPath, policyRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HISS_POLICY", policyPath)
	t.Setenv("HISS_INBOX", inbox)
	t.Setenv("HISS_OUTBOX", outbox)

	var out, errb bytes.Buffer
	code := hiss.Main([]string{"-once"}, &out, &errb, "dev", "none", "unknown")
	if code != 0 {
		t.Fatalf("want exit 0 for a single successful pass, got %d, stderr: %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(inbox, "processed", "ps-001.yaml")); err != nil {
		t.Error("--once must still run exactly one real Tick, not a no-op:", err)
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
		poll.Loop(ctx, poll.Config{Interval: 5 * time.Second, Timeout: 20 * time.Second}, tr.Tick)
	})
}
