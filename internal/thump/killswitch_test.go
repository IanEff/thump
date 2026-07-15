package thump_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/thump"
)

func writeSwitch(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "killswitch.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFileSwitch_ArmsOnlyWhenTheFileSaysArmedTrue(t *testing.T) {
	t.Parallel()
	s := thump.NewFileSwitch(writeSwitch(t, t.TempDir(), "armed: true\n"))
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Armed(context.Background()) {
		t.Error("a well-formed armed:true file must arm the switch")
	}
}

func TestFileSwitch_DisarmsWhenTheFileSaysArmedFalse(t *testing.T) {
	t.Parallel()
	s := thump.NewFileSwitch(writeSwitch(t, t.TempDir(), "armed: false\n"))
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Armed(context.Background()) {
		t.Error("armed:false must leave the switch disarmed")
	}
}

func TestFileSwitch_StartsDisarmedBeforeAnyReload(t *testing.T) {
	t.Parallel()
	s := thump.NewFileSwitch("/path/read/by/no/one")
	if s.Armed(context.Background()) {
		t.Error("a switch must start disarmed — the safe state before any config is read")
	}
}

func TestFileSwitch_DisarmsWhenTheFileIsMissing(t *testing.T) {
	t.Parallel()
	s := thump.NewFileSwitch(filepath.Join(t.TempDir(), "absent.yaml"))
	if err := s.Reload(context.Background()); err == nil {
		t.Error("a missing kill-switch file must surface an error for logging")
	}
	if s.Armed(context.Background()) {
		t.Error("a missing file must fail safe to disarmed")
	}
}

func TestFileSwitch_DisarmsOnMalformedContents(t *testing.T) {
	t.Parallel()
	s := thump.NewFileSwitch(writeSwitch(t, t.TempDir(), "armed: [unterminated\n"))
	if err := s.Reload(context.Background()); err == nil {
		t.Error("malformed contents must surface a parse error")
	}
	if s.Armed(context.Background()) {
		t.Error("malformed contents must fail safe to disarmed")
	}
}

func TestFileSwitch_ReloadPicksUpAFlipToDisarmed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := thump.NewFileSwitch(writeSwitch(t, dir, "armed: true\n"))
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Armed(context.Background()) {
		t.Fatal("setup: expected armed")
	}

	writeSwitch(t, dir, "armed: false\n") // an operator pulls the switch
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Armed(context.Background()) {
		t.Error("a reload must pick up a live flip to disarmed")
	}
}

func TestFileSwitch_AFailedReloadClearsAStaleArmedState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeSwitch(t, dir, "armed: true\n")
	s := thump.NewFileSwitch(path)
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Armed(context.Background()) {
		t.Fatal("setup: expected armed")
	}

	// the file vanishes mid-run (a ConfigMap remount, a bad deploy). The next
	// reload must not leave the armed state latched — any doubt disarms.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(context.Background()); err == nil {
		t.Error("expected an error when the file vanished")
	}
	if s.Armed(context.Background()) {
		t.Error("a failed reload must clear a stale armed state, never latch it")
	}
}
