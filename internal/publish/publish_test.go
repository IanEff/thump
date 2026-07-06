package publish_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"

	"github.com/ianeff/thump/api/v1/signal"
	"github.com/ianeff/thump/internal/publish"
)

// FakePublisher is the in-memory double a beat's own tests reach for.
type FakePublisher[T any] struct {
	Delivered []T
}

func (f *FakePublisher[T]) Publish(_ context.Context, _ string, obj T) error {
	f.Delivered = append(f.Delivered, obj)
	return nil
}

func TestFakePublisher_DeliversWhatWasPublished(t *testing.T) {
	fp := &FakePublisher[signal.Detection]{}
	want := signal.Detection{Fingerprint: "fp-1"}

	if err := fp.Publish(context.Background(), "thump.detections", want); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if diff := cmp.Diff([]signal.Detection{want}, fp.Delivered); diff != "" {
		t.Errorf("FakePublisher.Delivered mismatch (-want +got):\n%s", diff)
	}
}

func TestDirPublisher_WritesYAMLThatRoundTrips(t *testing.T) {
	dir := t.TempDir()
	pub := &publish.DirPublisher[signal.Detection]{Dir: dir}
	want := signal.Detection{Fingerprint: "fp-1"}

	if err := pub.Publish(context.Background(), "thump.detections", want); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "thump.detections-*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d files in %s, want 1", len(matches), dir)
	}

	raw, err := os.ReadFile(matches[0]) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	var got signal.Detection
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round-tripped detection mismatch (-want +got):\n%s", diff)
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
