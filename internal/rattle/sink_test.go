package rattle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/ianeff/thump/internal/rattle"
	"github.com/ianeff/thump/internal/signal"
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
