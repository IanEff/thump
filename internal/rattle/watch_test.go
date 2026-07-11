package rattle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
)

func TestLoadWatch_ParsesSLOsAndDeps(t *testing.T) {
	got, err := rattle.LoadWatch("testdata/watch.yaml")
	if err != nil {
		t.Fatalf("LoadWatch: %v", err)
	}
	want := []rattle.SLO{
		{
			ID: "test-availability", Object: "test-service", Tier: "tier-1", Objective: 0.995,
			ContractRef: "test-availability:v1",
			Dependencies: []rattle.Dependency{
				{Name: "test-dep-a", Role: "blocking"},
				{Name: "test-dep-b", Role: "optional"},
			},
		},
		{
			ID: "test-latency", Object: "test-service-2", Tier: "tier-2", Objective: 0.99,
			ContractRef:  "test-latency:v1",
			Dependencies: []rattle.Dependency{{Name: "test-dep-c", Role: "blocking"}},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LoadWatch (-want +got):\n%s", diff)
	}
}

func TestLoadWatch_EmptyFile_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("slos: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rattle.LoadWatch(path); err == nil {
		t.Error("LoadWatch with zero SLOs: want an error, got nil — an empty watch list is a misconfiguration, not a silent no-op poll (mirror C1's fail-loud discipline)")
	}
}

func TestLoadWatch_MissingFile_Errors(t *testing.T) {
	if _, err := rattle.LoadWatch("testdata/does-not-exist.yaml"); err == nil {
		t.Error("LoadWatch on a missing file: want an error, got nil")
	}
}
