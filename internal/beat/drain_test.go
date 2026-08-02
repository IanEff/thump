package beat_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/beat"
)

// widget is a stand-in domain type — beat knows nothing about any plane's
// real wire types, so DrainDir's tests exercise it against something local.
type widget struct {
	Name string `json:"name"`
}

func writeWidgetYAML(t *testing.T, dir, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("name: "+value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDrainDir_GoldenRun_EachValidFileReachesHandle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeWidgetYAML(t, dir, "a.yaml", "alpha")
	writeWidgetYAML(t, dir, "b.yaml", "beta")

	var got []string
	handle := func(path string, v widget) error {
		got = append(got, v.Name)
		return beat.Disposition(dir, path, "processed")
	}

	if err := beat.DrainDir(dir, "test", handle); err != nil {
		t.Fatal("golden run must not error:", err)
	}

	if len(got) != 2 {
		t.Fatalf("handle must run once per file, got %d calls: %v", len(got), got)
	}
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "processed", name)); err != nil {
			t.Errorf("handle-disposed file must move to processed/: %v", err)
		}
	}
}

func TestDrainDir_PoisonPill_QuarantinesAndSkipsHandle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeWidgetYAML(t, dir, "good.yaml", "alpha")
	if err := os.WriteFile(filepath.Join(dir, "poison.yaml"), []byte("name: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}

	var handled []string
	handle := func(path string, v widget) error {
		handled = append(handled, v.Name)
		return beat.Disposition(dir, path, "processed")
	}

	if err := beat.DrainDir(dir, "test", handle); err != nil {
		t.Fatal("one bad file must not fail the pass:", err)
	}

	if diff := cmp.Diff([]string{"alpha"}, handled); diff != "" {
		t.Error("poison must never reach handle (-want +got)", diff)
	}
	if _, err := os.Stat(filepath.Join(dir, "quarantine", "poison.yaml")); err != nil {
		t.Error("unparseable input must land in quarantine/:", err)
	}
}

func TestDrainDir_QuarantineFailure_WrapsTheQuarantineErrorNotTheUnmarshalError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "poison.yaml"), []byte("name: [not, {valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	// a plain file sits where quarantine/ needs to be a directory, so
	// os.MkdirAll inside Disposition fails with ENOTDIR.
	if err := os.WriteFile(filepath.Join(dir, "quarantine"), []byte("blocking"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := beat.DrainDir(dir, "test", func(string, widget) error { return nil })
	if err == nil {
		t.Fatal("a quarantine failure must surface as an error, got nil")
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("DrainDir must wrap the quarantine failure (ENOTDIR), not the unmarshal error: %v", err)
	}
}

func TestDrainDir_HandleError_AbortsThePassAndPropagatesVerbatim(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeWidgetYAML(t, dir, "a.yaml", "alpha")
	wantErr := errors.New("handle blew up")

	err := beat.DrainDir(dir, "test", func(string, widget) error { return wantErr })

	if !errors.Is(err, wantErr) {
		t.Errorf("DrainDir must return handle's error unwrapped, got %v", err)
	}
}

func TestDisposition_MovesTheFileIntoTheNamedSubdirectory(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"processed sub moves the file under processed/":   "processed",
		"quarantine sub moves the file under quarantine/": "quarantine",
		"a novel sub name is created on demand":           "stalled",
	}
	for name, sub := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			src := filepath.Join(dir, "f.yaml")
			if err := os.WriteFile(src, []byte("name: alpha"), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := beat.Disposition(dir, src, sub); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
				t.Error("the source path must no longer exist after Disposition")
			}
			if _, err := os.Stat(filepath.Join(dir, sub, "f.yaml")); err != nil {
				t.Errorf("file must land at dir/%s/f.yaml: %v", sub, err)
			}
		})
	}
}

func TestDisposition_AMkdirFailurePropagates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "f.yaml")
	if err := os.WriteFile(src, []byte("name: alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "quarantine"), []byte("blocking"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := beat.Disposition(dir, src, "quarantine")
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Disposition must surface the underlying MkdirAll failure, got %v", err)
	}
}
