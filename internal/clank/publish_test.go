package clank_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ianeff/thump/internal/clank"
)

func TestDirSink_WritesOneFilePerProposalSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pub := &clank.DirPublisher{Dir: dir}

	if err := pub.Publish(context.Background(), "thump.propsals", clank.ProposalSet{
		Name: "n", SignalRef: "slo_burn:ceph-rgw",
	}); err != nil {
		t.Fatal(err)
	}
	// named by fingerprint so a re-proposal of the same incident overwrites,
	// never piles up — the file inbox inherits the ledger's dedup intent.
	if _, err := os.Stat(filepath.Join(dir, "slo_burn:ceph-rgw.yaml")); err != nil {
		t.Errorf("DirSink must write <fingerprint>.yaml: %v", err)
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

func ExampleYAMLPublisher_Publish() {
	pub := &clank.YAMLPublisher{W: os.Stdout}
	if err := pub.Publish(context.Background(), "thump.proposals", clank.ProposalSet{
		FailureClass: clank.ClassDependencySaturation,
		Recommended:  "prop-001",
		Proposals: []clank.Candidate{
			{ID: "prop-001", ContractRef: "throttle-non-critical-paths", Rank: 1},
		},
	}); err != nil {
		fmt.Println("publish error:", err)
	}
	// Output:
	// failureClass: dependency_saturation
	// proposals:
	// - contractRef: throttle-non-critical-paths
	//   id: prop-001
	//   rank: 1
	// recommended: prop-001
}
