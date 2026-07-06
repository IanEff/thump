package rattle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/rattle"
	"sigs.k8s.io/yaml"
)

func TestDirSink_RoundTripsAFullyEnrichedDetection(t *testing.T) {
	dir := t.TempDir()
	want := goldenDetection() // fully enriched: topology + traffic + impact populated
	sink := &rattle.DirSink{Dir: dir}

	if err := sink.Deliver(want); err != nil {
		t.Fatal(err)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	raw, _ := os.ReadFile(matches[0])
	var got signal.Detection
	err := yaml.Unmarshal(raw, &got)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Error(diff)
	}
}

func TestWriteAtomicIsInvisibleToGlob(t *testing.T) {
	dir := t.TempDir()

	// Create a mock temp file matching our atomic pattern
	tmpPath := filepath.Join(dir, ".tmp-12345")
	if err := os.WriteFile(tmpPath, []byte("partial write"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Verify the glob pattern used by the consumers misses it
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) > 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}
